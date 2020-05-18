/*
Copyright (c) 2019 TriggerMesh, Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"sync"
	"sync/atomic"

	eventhub "github.com/Azure/azure-event-hubs-go"
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/lxc/lxd/shared/logger"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	eventingduckv1beta1 "knative.dev/eventing/pkg/apis/duck/v1beta1"
	eventingchannels "knative.dev/eventing/pkg/channel"
	"knative.dev/eventing/pkg/channel/multichannelfanout"
	"knative.dev/eventing/pkg/logging"

	"github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	"github.com/triggermesh/azure-event-channel/pkg/util"
)

// AzureDispatcher manages the state of Azure Streaming subscriptions
type AzureDispatcher struct {
	logger *zap.Logger

	receiver   *eventingchannels.MessageReceiver
	dispatcher *eventingchannels.MessageDispatcherImpl

	mux           sync.Mutex
	azureSessions map[eventingchannels.ChannelReference]client
	subscriptions map[eventingchannels.ChannelReference]map[subscriptionReference]bool

	config           atomic.Value
	hostToChannelMap atomic.Value

	hostToChannelMapLock sync.Mutex
}

type client struct {
	HubName             string
	AzureEventHubClient *util.AzureEventHubClient
}

// NewDispatcher returns a new AzureDispatcher.
func NewDispatcher(ctx context.Context) (*AzureDispatcher, error) {
	logger := logging.FromContext(ctx)

	d := &AzureDispatcher{
		logger:        logger,
		dispatcher:    eventingchannels.NewMessageDispatcher(logger),
		azureSessions: make(map[eventingchannels.ChannelReference]client),
		subscriptions: make(map[eventingchannels.ChannelReference]map[subscriptionReference]bool),
	}
	d.setHostToChannelMap(map[string]eventingchannels.ChannelReference{})

	receiver, err := eventingchannels.NewMessageReceiver(
		func(ctx context.Context, channel eventingchannels.ChannelReference, m binding.Message, transformers []binding.Transformer, _ nethttp.Header) error {
			logger.Sugar().Infof("Received message from %q channel", channel.String())
			// publish to azure
			event, err := binding.ToEvent(ctx, m)
			if err != nil {
				logger.Sugar().Errorf("Can't convert message to event: %v", err)
				return err
			}
			eventPayload, err := event.MarshalJSON()
			if err != nil {
				logger.Sugar().Errorf("Can't encode event: %v", err)
				return err
			}
			cRef := eventingchannels.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
			client, present := d.azureSessions[cRef]
			if !present {
				logger.Sugar().Errorf("Azure session not initialized")
				return err
			}
			if err := client.AzureEventHubClient.Hub.Send(ctx, eventhub.NewEvent(eventPayload)); err != nil {
				logger.Sugar().Errorf("Error during publish: %v", err)
				return err
			}
			logger.Sugar().Infof("Published to %q azure event hub", channel.String())
			return nil
		},
		logger,
		eventingchannels.ResolveMessageChannelFromHostHeader(d.getChannelReferenceFromHost))
	if err != nil {
		return nil, err
	}
	d.receiver = receiver
	return d, nil
}

//Start starts reciever
func (s *AzureDispatcher) Start(ctx context.Context) error {
	if s.receiver == nil {
		return fmt.Errorf("message receiver is not set")
	}
	return s.receiver.Start(ctx)
}

// UpdateSubscriptions creates/deletes the azure subscriptions based on channel.Spec.Subscribable.Subscribers
func (s *AzureDispatcher) UpdateSubscriptions(ctx context.Context, channel *v1alpha1.AzureChannel, isFinalizer bool) (map[eventingduckv1beta1.SubscriberSpec]error, error) {
	s.mux.Lock()
	defer s.mux.Unlock()

	failedToSubscribe := make(map[eventingduckv1beta1.SubscriberSpec]error)
	cRef := eventingchannels.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	if len(channel.Spec.Subscribers) == 0 || isFinalizer {
		s.logger.Sugar().Infof("Empty subscriptions for channel Ref: %v; unsubscribe all active subscriptions, if any", cRef)
		chMap, ok := s.subscriptions[cRef]
		if !ok {
			// nothing to do
			s.logger.Sugar().Infof("No channel Ref %v found in subscriptions map", cRef)
			return failedToSubscribe, nil
		}
		for sub := range chMap {
			s.unsubscribe(ctx, cRef, sub)
		}
		delete(s.subscriptions, cRef)
		return failedToSubscribe, nil
	}

	subscriptions := channel.Spec.Subscribers
	activeSubs := make(map[subscriptionReference]bool) // it's logically a set

	chMap, ok := s.subscriptions[cRef]
	if !ok {
		chMap = make(map[subscriptionReference]bool)
		s.subscriptions[cRef] = chMap
	}
	var errStrings []string
	for _, sub := range subscriptions {
		// check if the subscription already exist and do nothing in this case
		subRef := newSubscriptionReference(sub)
		if _, ok := chMap[subRef]; ok {
			activeSubs[subRef] = true
			s.logger.Sugar().Infof("Subscription: %v already active for channel: %v", sub, cRef)
			continue
		}
		// subscribe and update failedSubscription if subscribe fails
		err := s.subscribe(ctx, cRef, subRef)
		if err != nil {
			errStrings = append(errStrings, err.Error())
			s.logger.Sugar().Errorf("failed to subscribe (subscription:%q) to channel: %v. Error:%s", sub, cRef, err.Error())
			failedToSubscribe[sub] = err
			continue
		}
		chMap[subRef] = true
		activeSubs[subRef] = true
	}
	// Unsubscribe for deleted subscriptions
	for sub := range chMap {
		if ok := activeSubs[sub]; !ok {
			s.unsubscribe(ctx, cRef, sub)
		}
	}
	// delete the channel from s.subscriptions if chMap is empty
	if len(s.subscriptions[cRef]) == 0 {
		delete(s.subscriptions, cRef)
	}
	return failedToSubscribe, nil
}

func (s *AzureDispatcher) subscribe(ctx context.Context, channel eventingchannels.ChannelReference, subscription subscriptionReference) error {
	s.logger.Info("Subscribe to eventhub:", zap.Any("channel", channel), zap.Any("subscription", subscription))

	session, present := s.azureSessions[channel]
	if !present {
		s.logger.Error("Azure session not found:", zap.Any("channel", channel))
		return fmt.Errorf("Azure session for channel %q not found", channel.String())
	}

	handler := func(c context.Context, azureEvent *eventhub.Event) error {
		s.logger.Info("New event!", zap.Any("data", string(azureEvent.Data)))

		e := event.New(event.CloudEventsVersionV1)
		err := e.UnmarshalJSON(azureEvent.Data)
		if err != nil {
			s.logger.Error("Can't decode event", zap.Error(err))
			return err
		}
		err = e.Validate()
		if err != nil {
			s.logger.Error("Event validation error", zap.Error(err))
			return err
		}
		return s.dispatcher.DispatchMessage(
			context.Background(),
			binding.ToMessage(&e),
			nil,
			subscription.SubscriberURI.URL(),
			subscription.ReplyURI.URL(),
			nil,
		)
	}

	// listen to each partition of the Event Hub
	runtimeInfo, err := session.AzureEventHubClient.Hub.GetRuntimeInformation(ctx)
	if err != nil {
		return fmt.Errorf("GetRuntimeInformation failed: %v", err)
	}

	for _, partitionID := range runtimeInfo.PartitionIDs {
		_, err := session.AzureEventHubClient.Hub.Receive(ctx, partitionID, handler, eventhub.ReceiveWithLatestOffset())
		if err != nil {
			s.logger.Error("Receive func failed:", zap.Any("partitionID", partitionID), zap.Any("error", err))
		}
	}
	return nil
}

// should be called only while holding subscriptionsMux
func (s *AzureDispatcher) unsubscribe(ctx context.Context, channel eventingchannels.ChannelReference, subscription subscriptionReference) error {
	s.logger.Info("Unsubscribe from channel:", zap.Any("channel", channel), zap.Any("subscription", subscription))

	if _, ok := s.subscriptions[channel][subscription]; ok {
		delete(s.subscriptions[channel], subscription)
	}
	return nil
}

func (s *AzureDispatcher) getHostToChannelMap() map[string]eventingchannels.ChannelReference {
	return s.hostToChannelMap.Load().(map[string]eventingchannels.ChannelReference)
}

func (s *AzureDispatcher) setHostToChannelMap(hcMap map[string]eventingchannels.ChannelReference) {
	s.hostToChannelMap.Store(hcMap)
}

// UpdateHostToChannelMap will be called from the controller that watches azure channels.
// It will update internal hostToChannelMap which is used to resolve the hostHeader of the
// incoming request to the correct ChannelReference in the receiver function.
func (s *AzureDispatcher) UpdateHostToChannelMap(config *multichannelfanout.Config) error {
	if config == nil {
		return errors.New("nil config")
	}

	s.hostToChannelMapLock.Lock()
	defer s.hostToChannelMapLock.Unlock()

	hcMap, err := createHostToChannelMap(config)
	if err != nil {
		return err
	}

	s.setHostToChannelMap(hcMap)
	return nil
}

func createHostToChannelMap(config *multichannelfanout.Config) (map[string]eventingchannels.ChannelReference, error) {
	hcMap := make(map[string]eventingchannels.ChannelReference, len(config.ChannelConfigs))
	for _, cConfig := range config.ChannelConfigs {
		if cr, ok := hcMap[cConfig.HostName]; ok {
			return nil, fmt.Errorf(
				"duplicate hostName found. Each channel must have a unique host header. HostName:%s, channel:%s.%s, channel:%s.%s",
				cConfig.HostName,
				cConfig.Namespace,
				cConfig.Name,
				cr.Namespace,
				cr.Name)
		}
		hcMap[cConfig.HostName] = eventingchannels.ChannelReference{Name: cConfig.Name, Namespace: cConfig.Namespace}
	}
	return hcMap, nil
}

func (s *AzureDispatcher) getChannelReferenceFromHost(host string) (eventingchannels.ChannelReference, error) {
	chMap := s.getHostToChannelMap()
	cr, ok := chMap[host]
	if !ok {
		return cr, fmt.Errorf("Invalid HostName:%q. HostName not found in any of the watched azure channels", host)
	}
	return cr, nil
}

//AzureSessionExist checks if azure session exists
func (s *AzureDispatcher) AzureSessionExist(ctx context.Context, channel *v1alpha1.AzureChannel) bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := eventingchannels.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	_, present := s.azureSessions[cRef]
	return present
}

//CreateAzureSession creates azure session
func (s *AzureDispatcher) CreateAzureSession(ctx context.Context, channel *v1alpha1.AzureChannel, secret *corev1.Secret) error {
	if s.AzureSessionExist(ctx, channel) {
		return nil
	}

	logger.Infof("Create new Azure session for : %v", channel.Name)

	conn, err := s.newClient(ctx, channel.Spec.EventHubName, channel.Spec.EventHubRegion, secret)
	if err != nil {
		logger.Errorf("Error creating Azure session: %v", err)
		return err
	}

	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := eventingchannels.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}

	s.azureSessions[cRef] = client{
		HubName:             channel.Spec.EventHubName,
		AzureEventHubClient: conn,
	}

	return nil
}

//DeleteAzureSession removes azure session
func (s *AzureDispatcher) DeleteAzureSession(ctx context.Context, channel *v1alpha1.AzureChannel) {
	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := eventingchannels.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	if _, present := s.azureSessions[cRef]; present {
		delete(s.azureSessions, cRef)
	}
}

func (s *AzureDispatcher) newClient(ctx context.Context, hubName, region string, creds *corev1.Secret) (*util.AzureEventHubClient, error) {
	logger := logging.FromContext(ctx)

	logger.Info("Creating new Azure Eventhub Client")

	if creds == nil {
		return nil, fmt.Errorf("Credentials data is nil")
	}

	subscriptionID, present := creds.Data["subscription_id"]
	if !present {
		return nil, fmt.Errorf("\"subscription_id\" key is missing")
	}

	tenantID, present := creds.Data["tenant_id"]
	if !present {
		return nil, fmt.Errorf("\"tenant_id\" key is missing")
	}

	clientID, present := creds.Data["client_id"]
	if !present {
		return nil, fmt.Errorf("\"client_id\" key is missing")
	}

	clientSecret, present := creds.Data["client_secret"]
	if !present {
		return nil, fmt.Errorf("\"client_secret\" key is missing")
	}

	logger.Info(
		"New credentials in dispatcher!",
		zap.Any("subscriptionID", string(subscriptionID)),
		zap.Any("tenantID", string(tenantID)),
		zap.Any("clientID", string(clientID)),
		zap.Any("clientSecret", string(clientSecret)),
	)

	azureClient, err := util.Connect(ctx, string(subscriptionID), string(tenantID), string(clientID), string(clientSecret))
	if err != nil {
		logger.Error("Connected to Azure failed", zap.Error(err))
		return azureClient, err
	}

	hub, err := azureClient.CreateOrUpdateHub(ctx, hubName, region)
	if err != nil {
		logger.Error("CreateOrUpdateHub failed", zap.Error(err))
		return azureClient, err
	}

	logger.Info("New hub in dispatcher!", zap.Any("hub", hub))

	azureClient.Hub = hub

	return azureClient, nil
}
