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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	eventingduckv1beta1 "knative.dev/eventing/pkg/apis/duck/v1beta1"
	"knative.dev/eventing/pkg/channel/fanout"
	"knative.dev/eventing/pkg/channel/multichannelfanout"
	"knative.dev/eventing/pkg/logging"
	"knative.dev/pkg/client/injection/kube/informers/core/v1/secret"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"

	"github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	azureClientSet "github.com/triggermesh/azure-event-channel/pkg/client/clientset/internalclientset"
	"github.com/triggermesh/azure-event-channel/pkg/client/clientset/internalclientset/scheme"
	azureScheme "github.com/triggermesh/azure-event-channel/pkg/client/clientset/internalclientset/scheme"
	azureclient "github.com/triggermesh/azure-event-channel/pkg/client/injection/client"
	"github.com/triggermesh/azure-event-channel/pkg/client/injection/informers/messaging/v1alpha1/azurechannel"
	listers "github.com/triggermesh/azure-event-channel/pkg/client/listers/messaging/v1alpha1"
	"github.com/triggermesh/azure-event-channel/pkg/dispatcher"
)

const (
	// ReconcilerName is the name of the reconciler.
	ReconcilerName = "AzureChannels"

	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "azure-ch-dispatcher"

	finalizerName = controllerAgentName

	channelReconciled         = "ChannelReconciled"
	channelReconcileFailed    = "ChannelReconcileFailed"
	channelUpdateStatusFailed = "ChannelUpdateStatusFailed"
)

// Reconciler reconciles Azure Channels.
type Reconciler struct {
	recorder record.EventRecorder

	azureDispatcher *dispatcher.AzureDispatcher

	azureClientSet     azureClientSet.Interface
	azurechannelLister listers.AzureChannelLister
	secretLister       corev1listers.SecretLister
	impl               *controller.Impl
}

// Check that our Reconciler implements controller.Reconciler.
var _ controller.Reconciler = (*Reconciler)(nil)

func init() {
	// Add run types to the default Kubernetes Scheme so Events can be
	// logged for run types.
	_ = azureScheme.AddToScheme(scheme.Scheme)
}

// NewController initializes the controller and is called by the generated code.
// Registers event handlers to enqueue events.
func NewController(ctx context.Context, _ configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	dispatcher, _ := dispatcher.NewDispatcher(ctx)

	azurechannelInformer := azurechannel.Get(ctx)
	secretsInformer := secret.Get(ctx)

	r := &Reconciler{
		recorder: controller.GetEventRecorder(ctx),

		azureDispatcher: dispatcher,

		azureClientSet:     azureclient.Get(ctx),
		azurechannelLister: azurechannelInformer.Lister(),
		secretLister:       secretsInformer.Lister(),
	}
	r.impl = controller.NewImpl(r, logger.Sugar(), ReconcilerName)

	logger.Info("Setting up event handlers")

	// Watch for Azure channels.
	azurechannelInformer.Informer().AddEventHandler(controller.HandleAll(r.impl.Enqueue))

	logger.Info("Starting dispatcher")
	go func() {
		if err := dispatcher.Start(ctx); err != nil {
			logger.Error("Cannot start dispatcher", zap.Error(err))
		}
	}()

	return r.impl
}

func (r *Reconciler) Reconcile(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name.
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		logging.FromContext(ctx).Error("invalid resource key")
		return nil
	}

	// Get the AzureChannel resource with this namespace/name.
	original, err := r.azurechannelLister.AzureChannels(namespace).Get(name)
	if apierrs.IsNotFound(err) {
		// The resource may no longer exist, in which case we stop processing.
		return nil
	} else if err != nil {
		return err
	}

	if !original.Status.IsReady() {
		return fmt.Errorf("Channel is not ready. Cannot configure and update subscriber status")
	}

	// Don't modify the informers copy.
	azureChannel := original.DeepCopy()

	reconcileErr := r.reconcile(ctx, azureChannel)
	if reconcileErr != nil {
		logging.FromContext(ctx).Error("Error reconciling AzureChannel", zap.Error(reconcileErr))
	} else {
		logging.FromContext(ctx).Debug("AzureChannel reconciled")
	}

	// TODO: Should this check for subscribable status rather than entire status?
	if _, updateStatusErr := r.updateStatus(ctx, azureChannel); updateStatusErr != nil {
		logging.FromContext(ctx).Error("Failed to update AzureChannel status", zap.Error(updateStatusErr))
		r.recorder.Eventf(azureChannel, corev1.EventTypeWarning, channelUpdateStatusFailed, "Failed to update KinesisChannel's status: %v", updateStatusErr)
		return updateStatusErr
	}
	return reconcileErr
}

func (r *Reconciler) reconcile(ctx context.Context, azureChannel *v1alpha1.AzureChannel) error {
	// TODO update dispatcher API and use Channelable or AzureChannel.

	// See if the channel has been deleted.
	if azureChannel.DeletionTimestamp != nil {
		if _, err := r.azureDispatcher.UpdateSubscriptions(ctx, azureChannel, true); err != nil {
			logging.FromContext(ctx).Error("Error updating subscriptions", zap.Any("channel", azureChannel), zap.Error(err))
			return err
		}
		r.azureDispatcher.DeleteAzureSession(ctx, azureChannel)
		removeFinalizer(azureChannel)
		_, err := r.azureClientSet.MessagingV1alpha1().AzureChannels(azureChannel.Namespace).Update(azureChannel)
		return err
	}

	// If we are adding the finalizer for the first time, then ensure that finalizer is persisted
	// before manipulating Azure.
	if err := r.ensureFinalizer(azureChannel); err != nil {
		return err
	}

	if !r.azureDispatcher.AzureSessionExist(ctx, azureChannel) {
		secret, err := r.secretLister.Secrets(azureChannel.Namespace).Get(azureChannel.Spec.SecretName)
		if err != nil {
			return err
		}
		if err := r.azureDispatcher.CreateAzureSession(ctx, azureChannel, secret); err != nil {
			return err
		}
	}

	// Try to subscribe.
	failedSubscriptions, err := r.azureDispatcher.UpdateSubscriptions(ctx, azureChannel, false)
	if err != nil {
		logging.FromContext(ctx).Error("Error updating subscriptions", zap.Any("channel", azureChannel), zap.Error(err))
		return err
	}
	azureChannel.Status.SubscribableStatus = r.createSubscribableStatus(azureChannel.Spec.SubscribableSpec, failedSubscriptions)
	if len(failedSubscriptions) > 0 {
		var b strings.Builder
		for _, subError := range failedSubscriptions {
			b.WriteString("\n")
			b.WriteString(subError.Error())
		}
		errMsg := b.String()
		logging.FromContext(ctx).Error(errMsg)
		return fmt.Errorf(errMsg)
	}

	azureChannels, err := r.azurechannelLister.List(labels.Everything())
	if err != nil {
		logging.FromContext(ctx).Error("Error listing azure channels")
		return err
	}

	channels := make([]*v1alpha1.AzureChannel, 0)
	for _, nc := range azureChannels {
		if nc.Status.IsReady() {
			channels = append(channels, nc)
		}
	}

	config := r.newConfigFromAzureChannels(channels)

	if err := r.azureDispatcher.UpdateHostToChannelMap(config); err != nil {
		logging.FromContext(ctx).Error("Error updating host to channel map", zap.Error(err))
		return err
	}
	return nil
}

func (r *Reconciler) createSubscribableStatus(subscribable eventingduckv1beta1.SubscribableSpec, failedSubscriptions map[eventingduckv1beta1.SubscriberSpec]error) eventingduckv1beta1.SubscribableStatus {
	if len(subscribable.Subscribers) == 0 {
		return eventingduckv1beta1.SubscribableStatus{}
	}
	subscriberStatus := make([]eventingduckv1beta1.SubscriberStatus, 0)
	for _, sub := range subscribable.Subscribers {
		status := eventingduckv1beta1.SubscriberStatus{
			UID:                sub.UID,
			ObservedGeneration: sub.Generation,
			Ready:              corev1.ConditionTrue,
		}
		if err, ok := failedSubscriptions[sub]; ok {
			status.Ready = corev1.ConditionFalse
			status.Message = err.Error()
		}
		subscriberStatus = append(subscriberStatus, status)
	}
	return eventingduckv1beta1.SubscribableStatus{
		Subscribers: subscriberStatus,
	}
}
func (r *Reconciler) updateStatus(ctx context.Context, desired *v1alpha1.AzureChannel) (*v1alpha1.AzureChannel, error) {
	nc, err := r.azurechannelLister.AzureChannels(desired.Namespace).Get(desired.Name)
	if err != nil {
		return nil, err
	}

	if reflect.DeepEqual(nc.Status, desired.Status) {
		return nc, nil
	}

	// Don't modify the informers copy.
	existing := nc.DeepCopy()
	existing.Status = desired.Status

	return r.azureClientSet.MessagingV1alpha1().AzureChannels(desired.Namespace).UpdateStatus(existing)
}

func (r *Reconciler) ensureFinalizer(channel *v1alpha1.AzureChannel) error {
	finalizers := sets.NewString(channel.Finalizers...)
	if finalizers.Has(finalizerName) {
		return nil
	}

	mergePatch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"finalizers":      append(channel.Finalizers, finalizerName),
			"resourceVersion": channel.ResourceVersion,
		},
	}

	patch, err := json.Marshal(mergePatch)
	if err != nil {
		return err
	}

	_, err = r.azureClientSet.MessagingV1alpha1().AzureChannels(channel.Namespace).Patch(channel.Name, types.MergePatchType, patch)
	return err
}

func removeFinalizer(channel *v1alpha1.AzureChannel) {
	finalizers := sets.NewString(channel.Finalizers...)
	finalizers.Delete(finalizerName)
	channel.Finalizers = finalizers.List()
}

// newConfigFromAzureChannels creates a new Config from the list of azure channels.
func (r *Reconciler) newConfigFromAzureChannels(channels []*v1alpha1.AzureChannel) *multichannelfanout.Config {
	cc := make([]multichannelfanout.ChannelConfig, 0)
	for _, c := range channels {
		channelConfig := r.newChannelConfigFromAzureChannel(c)
		cc = append(cc, *channelConfig)
	}
	return &multichannelfanout.Config{
		ChannelConfigs: cc,
	}
}

// newConfigFromAzureChannels creates a new Config from the list of azure channels.
func (r *Reconciler) newChannelConfigFromAzureChannel(c *v1alpha1.AzureChannel) *multichannelfanout.ChannelConfig {
	channelConfig := multichannelfanout.ChannelConfig{
		Namespace: c.Namespace,
		Name:      c.Name,
		HostName:  c.Status.Address.Hostname,
	}
	if c.Spec.Subscribers != nil {
		channelConfig.FanoutConfig = fanout.Config{
			AsyncHandler:  true,
			Subscriptions: c.Spec.Subscribers,
		}
	}
	return &channelConfig
}
