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

	eventingduck "github.com/knative/eventing/pkg/apis/duck/v1alpha1"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	"github.com/knative/eventing/pkg/logging"
	"github.com/knative/pkg/controller"
	"github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	messaginginformers "github.com/triggermesh/azure-event-channel/pkg/client/informers/externalversions/messaging/v1alpha1"
	listers "github.com/triggermesh/azure-event-channel/pkg/client/listers/messaging/v1alpha1"
	"github.com/triggermesh/azure-event-channel/pkg/dispatcher"
	"github.com/triggermesh/azure-event-channel/pkg/reconciler"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

const (
	// ReconcilerName is the name of the reconciler.
	ReconcilerName = "AzureChannels"

	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "azure-ch-dispatcher"

	finalizerName = controllerAgentName
)

// Reconciler reconciles Azure Channels.
type Reconciler struct {
	*reconciler.Base

	azureDispatcher *dispatcher.SubscriptionsSupervisor

	azurechannelLister   listers.AzureChannelLister
	azurechannelInformer cache.SharedIndexInformer
	impl                 *controller.Impl
}

// Check that our Reconciler implements controller.Reconciler.
var _ controller.Reconciler = (*Reconciler)(nil)

// NewController initializes the controller and is called by the generated code.
// Registers event handlers to enqueue events.
func NewController(
	opt reconciler.Options,
	azureDispatcher *dispatcher.SubscriptionsSupervisor,
	azurechannelInformer messaginginformers.AzureChannelInformer,
) *controller.Impl {

	r := &Reconciler{
		Base:                 reconciler.NewBase(opt, controllerAgentName),
		azureDispatcher:      azureDispatcher,
		azurechannelLister:   azurechannelInformer.Lister(),
		azurechannelInformer: azurechannelInformer.Informer(),
	}
	r.impl = controller.NewImpl(r, r.Logger, ReconcilerName)

	r.Logger.Info("Setting up event handlers")

	// Watch for Azure channels.
	azurechannelInformer.Informer().AddEventHandler(controller.HandleAll(r.impl.Enqueue))

	return r.impl
}

func (r *Reconciler) Reconcile(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name.
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		logging.FromContext(ctx).Error("invalid resource key")
		return nil
	}

	logging.FromContext(ctx).Info("Azure Channel Info", zap.Any("namespace", namespace), zap.Any("name", name))

	// Get the AzureChannel resource with this namespace/name.
	original, err := r.azurechannelLister.AzureChannels(namespace).Get(name)
	if apierrs.IsNotFound(err) {
		// The resource may no longer exist, in which case we stop processing.
		logging.FromContext(ctx).Error("AzureChannel key in work queue no longer exists")
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
		return updateStatusErr
	}
	return nil
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
		_, err := r.AzureClientSet.MessagingV1alpha1().AzureChannels(azureChannel.Namespace).Update(azureChannel)
		return err
	}

	// If we are adding the finalizer for the first time, then ensure that finalizer is persisted
	// before manipulating Azure.
	if err := r.ensureFinalizer(azureChannel); err != nil {
		return err
	}

	if !r.azureDispatcher.AzureSessionExist(ctx, azureChannel) {
		secret, err := r.KubeClientSet.CoreV1().Secrets(azureChannel.Namespace).Get(azureChannel.Spec.SecretName, metav1.GetOptions{})
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
	azureChannel.Status.SubscribableStatus = r.createSubscribableStatus(azureChannel.Spec.Subscribable, failedSubscriptions)
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

	channels := make([]eventingv1alpha1.Channel, 0)
	for _, nc := range azureChannels {
		if nc.Status.IsReady() {
			channels = append(channels, *toChannel(nc))
		}
	}

	if err := r.azureDispatcher.UpdateHostToChannelMap(ctx, channels); err != nil {
		logging.FromContext(ctx).Error("Error updating host to channel map", zap.Error(err))
		return err
	}
	return nil
}

func (r *Reconciler) createSubscribableStatus(subscribable *eventingduck.Subscribable, failedSubscriptions map[eventingduck.SubscriberSpec]error) *eventingduck.SubscribableStatus {
	if subscribable == nil {
		return nil
	}
	subscriberStatus := make([]eventingduck.SubscriberStatus, 0)
	for _, sub := range subscribable.Subscribers {
		status := eventingduck.SubscriberStatus{
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
	return &eventingduck.SubscribableStatus{
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

	return r.AzureClientSet.MessagingV1alpha1().AzureChannels(desired.Namespace).UpdateStatus(existing)
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

	_, err = r.AzureClientSet.MessagingV1alpha1().AzureChannels(channel.Namespace).Patch(channel.Name, types.MergePatchType, patch)
	return err
}

func removeFinalizer(channel *v1alpha1.AzureChannel) {
	finalizers := sets.NewString(channel.Finalizers...)
	finalizers.Delete(finalizerName)
	channel.Finalizers = finalizers.List()
}

func toChannel(azureChannel *v1alpha1.AzureChannel) *eventingv1alpha1.Channel {
	channel := &eventingv1alpha1.Channel{
		ObjectMeta: v1.ObjectMeta{
			Name:      azureChannel.Name,
			Namespace: azureChannel.Namespace,
		},
		Spec: eventingv1alpha1.ChannelSpec{
			Subscribable: azureChannel.Spec.Subscribable,
		},
	}
	if azureChannel.Status.Address != nil {
		channel.Status = eventingv1alpha1.ChannelStatus{
			Address: *azureChannel.Status.Address,
		}
	}
	return channel
}
