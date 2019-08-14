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
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-event-hubs-go"
	eventingduck "github.com/knative/eventing/pkg/apis/duck/v1alpha1"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	"github.com/knative/eventing/pkg/logging"
	"github.com/knative/eventing/pkg/provisioners"
	"github.com/lxc/lxd/shared/logger"
	"github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"sync"
	"sync/atomic"
)

// SubscriptionsSupervisor manages the state of Azure Streaming subscriptions
type SubscriptionsSupervisor struct {
	logger *zap.Logger

	receiver   *provisioners.MessageReceiver
	dispatcher *provisioners.MessageDispatcher

	mux           sync.Mutex
	azureSessions map[provisioners.ChannelReference]azurehub
	subscriptions map[provisioners.ChannelReference]map[subscriptionReference]bool

	hostToChannelMap atomic.Value
}

type azurehub struct {
	Name             string
	hubClient        *eventhub.Hub
	hubManagerClient *eventhub.HubManager
}

// NewDispatcher returns a new SubscriptionsSupervisor.
func NewDispatcher(logger *zap.Logger) (*SubscriptionsSupervisor, error) {
	d := &SubscriptionsSupervisor{
		logger:        logger,
		dispatcher:    provisioners.NewMessageDispatcher(logger.Sugar()),
		azureSessions: make(map[provisioners.ChannelReference]azurehub),
		subscriptions: make(map[provisioners.ChannelReference]map[subscriptionReference]bool),
	}
	d.setHostToChannelMap(map[string]provisioners.ChannelReference{})
	receiver, err := provisioners.NewMessageReceiver(
		// not sure where to get context
		createReceiverFunction(context.Background(), d, logger.Sugar()),
		logger.Sugar(),
		provisioners.ResolveChannelFromHostHeader(provisioners.ResolveChannelFromHostFunc(d.getChannelReferenceFromHost)))
	if err != nil {
		return nil, err
	}
	d.receiver = receiver
	return d, nil
}

func createReceiverFunction(ctx context.Context, s *SubscriptionsSupervisor, logger *zap.SugaredLogger) func(provisioners.ChannelReference, *provisioners.Message) error {
	return func(channel provisioners.ChannelReference, m *provisioners.Message) error {
		logger.Infof("Received message from %q channel", channel.String())
		// publish to azure
		message, err := json.Marshal(m)
		if err != nil {
			logger.Errorf("Error during marshaling of the message: %v", err)
			return err
		}
		cRef := provisioners.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
		hc, present := s.azureSessions[cRef]
		if !present {
			logger.Errorf("Azure session not initialized")
			return err
		}
		if err := hc.hubClient.Send(ctx, eventhub.NewEvent(message)); err != nil {
			logger.Errorf("Error during publish: %v", err)
			return err
		}
		logger.Infof("Published [%s] : '%s'", channel.String(), m.Headers)
		return nil
	}
}

//Start starts reciever
func (s *SubscriptionsSupervisor) Start(stopCh <-chan struct{}) error {
	return s.receiver.Start(stopCh)
}

// UpdateSubscriptions creates/deletes the azure subscriptions based on channel.Spec.Subscribable.Subscribers
func (s *SubscriptionsSupervisor) UpdateSubscriptions(ctx context.Context, channel *v1alpha1.AzureChannel, isFinalizer bool) (map[eventingduck.SubscriberSpec]error, error) {
	s.mux.Lock()
	defer s.mux.Unlock()

	failedToSubscribe := make(map[eventingduck.SubscriberSpec]error)
	cRef := provisioners.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	if channel.Spec.Subscribable == nil || isFinalizer {
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

	subscriptions := channel.Spec.Subscribable.Subscribers
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

func (s *SubscriptionsSupervisor) subscribe(ctx context.Context, channel provisioners.ChannelReference, subscription subscriptionReference) error {
	s.logger.Info("Subscribe to eventhub:", zap.Any("channel", channel), zap.Any("subscription", subscription))

	session, present := s.azureSessions[channel]
	if !present {
		s.logger.Error("Azure session not found:", zap.Any("channel", channel))
		return fmt.Errorf("Azure session for channel %q not found", channel.String())
	}

	handler := func(c context.Context, event *eventhub.Event) error {
		s.logger.Info("New event!", zap.Any("data", string(event.Data)))

		return s.dispatcher.DispatchMessage(&provisioners.Message{
			Payload: event.Data,
		}, subscription.SubscriberURI, subscription.ReplyURI, provisioners.DispatchDefaults{
			Namespace: channel.Namespace,
		})
	}

	s.logger.Info("Hub to get runtime info about", zap.Any("hub", session.hubClient))

	// listen to each partition of the Event Hub
	runtimeInfo, err := session.hubClient.GetRuntimeInformation(ctx)
	if err != nil {
		return fmt.Errorf("GetRuntimeInformation failed: %v", err)
	}

	for _, partitionID := range runtimeInfo.PartitionIDs {
		_, err := session.hubClient.Receive(ctx, partitionID, handler, eventhub.ReceiveWithLatestOffset())
		if err != nil {
			s.logger.Error("Receive func failed:", zap.Any("partitionID", partitionID), zap.Any("error", err))
		}
	}
	return nil
}

// should be called only while holding subscriptionsMux
func (s *SubscriptionsSupervisor) unsubscribe(ctx context.Context, channel provisioners.ChannelReference, subscription subscriptionReference) error {
	s.logger.Info("Unsubscribe from channel:", zap.Any("channel", channel), zap.Any("subscription", subscription))

	if _, ok := s.subscriptions[channel][subscription]; ok {
		delete(s.subscriptions[channel], subscription)
	}
	return nil
}

func (s *SubscriptionsSupervisor) getHostToChannelMap() map[string]provisioners.ChannelReference {
	return s.hostToChannelMap.Load().(map[string]provisioners.ChannelReference)
}

func (s *SubscriptionsSupervisor) setHostToChannelMap(hcMap map[string]provisioners.ChannelReference) {
	s.hostToChannelMap.Store(hcMap)
}

// UpdateHostToChannelMap will be called from the controller that watches azure channels.
// It will update internal hostToChannelMap which is used to resolve the hostHeader of the
// incoming request to the correct ChannelReference in the receiver function.
func (s *SubscriptionsSupervisor) UpdateHostToChannelMap(ctx context.Context, chanList []eventingv1alpha1.Channel) error {
	hostToChanMap, err := provisioners.NewHostNameToChannelRefMap(chanList)
	if err != nil {
		logging.FromContext(ctx).Info("UpdateHostToChannelMap: Error occurred when creating the new hostToChannel map.", zap.Error(err))
		return err
	}
	s.setHostToChannelMap(hostToChanMap)
	logging.FromContext(ctx).Info("hostToChannelMap updated successfully.")
	return nil
}

func (s *SubscriptionsSupervisor) getChannelReferenceFromHost(host string) (provisioners.ChannelReference, error) {
	chMap := s.getHostToChannelMap()
	cr, ok := chMap[host]
	if !ok {
		return cr, fmt.Errorf("Invalid HostName:%q. HostName not found in any of the watched azure channels", host)
	}
	return cr, nil
}

//AzureSessionExist checks if azure session exists
func (s *SubscriptionsSupervisor) AzureSessionExist(ctx context.Context, channel *v1alpha1.AzureChannel) bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := provisioners.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	_, present := s.azureSessions[cRef]
	return present
}

//CreateAzureSession creates azure session
func (s *SubscriptionsSupervisor) CreateAzureSession(ctx context.Context, channel *v1alpha1.AzureChannel, secret *corev1.Secret) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := provisioners.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	_, present := s.azureSessions[cRef]
	if !present {
		hubManager, hub, err := s.newClients(secret)
		if err != nil {
			logger.Errorf("Error creating Azure session: %v", err)
			return err
		}

		s.azureSessions[cRef] = azurehub{
			Name:             channel.Spec.EventHubName,
			hubManagerClient: hubManager,
			hubClient:        hub,
		}
	}
	return nil
}

//DeleteAzureSession removes azure session
func (s *SubscriptionsSupervisor) DeleteAzureSession(ctx context.Context, channel *v1alpha1.AzureChannel) {
	s.mux.Lock()
	defer s.mux.Unlock()
	cRef := provisioners.ChannelReference{Namespace: channel.Namespace, Name: channel.Name}
	if _, present := s.azureSessions[cRef]; present {
		delete(s.azureSessions, cRef)
	}
}

func (s *SubscriptionsSupervisor) newClients(creds *corev1.Secret) (*eventhub.HubManager, *eventhub.Hub, error) {
	if creds == nil {
		return nil, nil, fmt.Errorf("Credentials data is nil")
	}
	connStr, present := creds.Data["_connection_string"]
	if !present {
		return nil, nil, fmt.Errorf("\"_connection_string\" key is missing")
	}

	hubManager, err := eventhub.NewHubManagerFromConnectionString(string(connStr))
	if err != nil {
		return nil, nil, fmt.Errorf("Could not create new hub manager ", err)
	}

	hub, err := eventhub.NewHubFromConnectionString(string(connStr))
	if err != nil {
		return nil, nil, fmt.Errorf("Could not create new hub ", err)
	}

	return hubManager, hub, nil
}
