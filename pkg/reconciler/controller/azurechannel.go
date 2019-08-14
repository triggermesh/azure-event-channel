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
	"fmt"
	"reflect"
	"time"

	"github.com/Azure/azure-event-hubs-go"

	eh "github.com/Azure/azure-sdk-for-go/services/eventhub/mgmt/2017-04-01/eventhub"
	"github.com/knative/eventing/pkg/logging"
	"github.com/knative/eventing/pkg/reconciler/names"
	"github.com/knative/pkg/apis"
	"github.com/knative/pkg/controller"
	"github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	messaginginformers "github.com/triggermesh/azure-event-channel/pkg/client/informers/externalversions/messaging/v1alpha1"
	listers "github.com/triggermesh/azure-event-channel/pkg/client/listers/messaging/v1alpha1"
	"github.com/triggermesh/azure-event-channel/pkg/reconciler"
	"github.com/triggermesh/azure-event-channel/pkg/reconciler/controller/resources"
	"github.com/triggermesh/azure-event-channel/pkg/util"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1informers "k8s.io/client-go/informers/apps/v1"
	corev1informers "k8s.io/client-go/informers/core/v1"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	// ReconcilerName is the name of the reconciler.
	ReconcilerName = "AzureChannels"

	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "azure-ch-controller"

	finalizerName = controllerAgentName

	// Name of the corev1.Events emitted from the reconciliation process.
	channelReconciled         = "ChannelReconciled"
	channelReconcileFailed    = "ChannelReconcileFailed"
	channelUpdateStatusFailed = "ChannelUpdateStatusFailed"
)

// Reconciler reconciles Azure Channels.
type Reconciler struct {
	*reconciler.Base

	dispatcherNamespace      string
	dispatcherDeploymentName string
	dispatcherServiceName    string

	azurechannelLister   listers.AzureChannelLister
	azurechannelInformer cache.SharedIndexInformer
	deploymentLister     appsv1listers.DeploymentLister
	serviceLister        corev1listers.ServiceLister
	endpointsLister      corev1listers.EndpointsLister
	impl                 *controller.Impl
}

var (
	deploymentGVK = appsv1.SchemeGroupVersion.WithKind("Deployment")
	serviceGVK    = corev1.SchemeGroupVersion.WithKind("Service")
)

// Check that our Reconciler implements controller.Reconciler.
var _ controller.Reconciler = (*Reconciler)(nil)

// Check that our Reconciler implements cache.ResourceEventHandler
var _ cache.ResourceEventHandler = (*Reconciler)(nil)

// NewController initializes the controller and is called by the generated code.
// Registers event handlers to enqueue events.
func NewController(
	opt reconciler.Options,
	dispatcherNamespace string,
	dispatcherDeploymentName string,
	dispatcherServiceName string,
	azurechannelInformer messaginginformers.AzureChannelInformer,
	deploymentInformer appsv1informers.DeploymentInformer,
	serviceInformer corev1informers.ServiceInformer,
	endpointsInformer corev1informers.EndpointsInformer,
) *controller.Impl {

	r := &Reconciler{
		Base:                     reconciler.NewBase(opt, controllerAgentName),
		dispatcherNamespace:      dispatcherNamespace,
		dispatcherDeploymentName: dispatcherDeploymentName,
		dispatcherServiceName:    dispatcherServiceName,
		azurechannelLister:       azurechannelInformer.Lister(),
		azurechannelInformer:     azurechannelInformer.Informer(),
		deploymentLister:         deploymentInformer.Lister(),
		serviceLister:            serviceInformer.Lister(),
		endpointsLister:          endpointsInformer.Lister(),
	}
	r.impl = controller.NewImpl(r, r.Logger, ReconcilerName)

	r.Logger.Info("Setting up event handlers")
	azurechannelInformer.Informer().AddEventHandler(controller.HandleAll(r.impl.Enqueue))

	// Set up watches for dispatcher resources we care about, since any changes to these
	// resources will affect our Channels. So, set up a watch here, that will cause
	// a global Resync for all the channels to take stock of their health when these change.
	deploymentInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterWithNameAndNamespace(dispatcherNamespace, dispatcherDeploymentName),
		Handler:    r,
	})
	serviceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterWithNameAndNamespace(dispatcherNamespace, dispatcherServiceName),
		Handler:    r,
	})
	endpointsInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: controller.FilterWithNameAndNamespace(dispatcherNamespace, dispatcherServiceName),
		Handler:    r,
	})
	return r.impl
}

// cache.ResourceEventHandler implementation.
// These 3 functions just cause a Global Resync of the channels, because any changes here
// should be reflected onto the channels.
func (r *Reconciler) OnAdd(obj interface{}) {
	r.impl.GlobalResync(r.azurechannelInformer)
}

func (r *Reconciler) OnUpdate(old, new interface{}) {
	r.impl.GlobalResync(r.azurechannelInformer)
}

func (r *Reconciler) OnDelete(obj interface{}) {
	r.impl.GlobalResync(r.azurechannelInformer)
}

// Reconcile compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the AzureChannel resource
// with the current status of the resource.
func (r *Reconciler) Reconcile(ctx context.Context, key string) error {
	// Convert the namespace/name string into a distinct namespace and name.
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		logging.FromContext(ctx).Error("invalid resource key")
		return nil
	}

	// Get the AzureChannels resource with this namespace/name.
	original, err := r.azurechannelLister.AzureChannels(namespace).Get(name)
	if apierrs.IsNotFound(err) {
		// The resource may no longer exist, in which case we stop processing.
		logging.FromContext(ctx).Error("AzureChannel key in work queue no longer exists")
		return nil
	} else if err != nil {
		return err
	}

	// Don't modify the informers copy.
	channel := original.DeepCopy()

	// Reconcile this copy of the AzureChannels and then write back any status updates regardless of
	// whether the reconcile error out.
	reconcileErr := r.reconcile(ctx, channel)
	if reconcileErr != nil {
		logging.FromContext(ctx).Error("Error reconciling AzureChannel", zap.Error(reconcileErr))
		r.Recorder.Eventf(channel, corev1.EventTypeWarning, channelReconcileFailed, "AzureChannel reconciliation failed: %v", reconcileErr)
	} else {
		logging.FromContext(ctx).Debug("AzureChannel reconciled")
		r.Recorder.Event(channel, corev1.EventTypeNormal, channelReconciled, "AzureChannel reconciled")
	}

	if _, updateStatusErr := r.updateStatus(ctx, channel); updateStatusErr != nil {
		logging.FromContext(ctx).Error("Failed to update AzureChannel status", zap.Error(updateStatusErr))
		r.Recorder.Eventf(channel, corev1.EventTypeWarning, channelUpdateStatusFailed, "Failed to update AzureChannel's status: %v", updateStatusErr)
		return updateStatusErr
	}

	// Requeue if the resource is not ready
	return reconcileErr
}

func (r *Reconciler) reconcile(ctx context.Context, kc *v1alpha1.AzureChannel) error {
	kc.Status.InitializeConditions()

	logger := logging.FromContext(ctx)

	// See if the channel has been deleted.
	if kc.DeletionTimestamp != nil {
		if kc.Status.GetCondition(v1alpha1.AzureChannelConditionStreamReady).IsTrue() {
			creds, err := r.KubeClientSet.CoreV1().Secrets(kc.Namespace).Get(kc.Spec.EventHubName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			hubManager, _, err := r.hubClients(creds)
			if err != nil {
				return err
			}
			if err := r.removeAzureHub(ctx, kc.Spec.EventHubName, hubManager); err != nil {
				return err
			}
		}
		// K8s garbage collection will delete the K8s service for this channel.
		return nil
	}

	// We reconcile the status of the Channel by looking at:
	// 1. Dispatcher Deployment for it's readiness.
	// 2. Dispatcher k8s Service for it's existence.
	// 3. Dispatcher endpoints to ensure that there's something backing the Service.
	// 4. K8s service representing the channel that will use ExternalName to point to the Dispatcher k8s service.

	// Get the Dispatcher Deployment and propagate the status to the Channel
	d, err := r.deploymentLister.Deployments(r.dispatcherNamespace).Get(r.dispatcherDeploymentName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			kc.Status.MarkDispatcherFailed("DispatcherDeploymentDoesNotExist", "Dispatcher Deployment does not exist")
		} else {
			logger.Error("Unable to get the dispatcher Deployment", zap.Error(err))
			kc.Status.MarkDispatcherFailed("DispatcherDeploymentGetFailed", "Failed to get dispatcher Deployment")
		}
		return err
	}
	kc.Status.PropagateDispatcherStatus(&d.Status)

	// Get the Dispatcher Service and propagate the status to the Channel in case it does not exist.
	// We don't do anything with the service because it's status contains nothing useful, so just do
	// an existence check. Then below we check the endpoints targeting it.
	_, err = r.serviceLister.Services(r.dispatcherNamespace).Get(r.dispatcherServiceName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			kc.Status.MarkServiceFailed("DispatcherServiceDoesNotExist", "Dispatcher Service does not exist")
		} else {
			logger.Error("Unable to get the dispatcher service", zap.Error(err))
			kc.Status.MarkServiceFailed("DispatcherServiceGetFailed", "Failed to get dispatcher service")
		}
		return err
	}
	kc.Status.MarkServiceTrue()

	// Get the Dispatcher Service Endpoints and propagate the status to the Channel
	// endpoints has the same name as the service, so not a bug.
	e, err := r.endpointsLister.Endpoints(r.dispatcherNamespace).Get(r.dispatcherServiceName)
	if err != nil {
		if apierrs.IsNotFound(err) {
			kc.Status.MarkEndpointsFailed("DispatcherEndpointsDoesNotExist", "Dispatcher Endpoints does not exist")
		} else {
			logger.Error("Unable to get the dispatcher endpoints", zap.Error(err))
			kc.Status.MarkEndpointsFailed("DispatcherEndpointsGetFailed", "Failed to get dispatcher endpoints")
		}
		return err
	}

	if len(e.Subsets) == 0 {
		logger.Error("No endpoints found for Dispatcher service", zap.Error(err))
		kc.Status.MarkEndpointsFailed("DispatcherEndpointsNotReady", "There are no endpoints ready for Dispatcher service")
		return fmt.Errorf("there are no endpoints ready for Dispatcher service %s", r.dispatcherServiceName)
	}
	kc.Status.MarkEndpointsTrue()

	// Reconcile the k8s service representing the actual Channel. It points to the Dispatcher service via ExternalName
	svc, err := r.reconcileChannelService(ctx, kc)
	if err != nil {
		kc.Status.MarkChannelServiceFailed("ChannelServiceFailed", fmt.Sprintf("Channel Service failed: %s", err))
		return err
	}
	kc.Status.MarkChannelServiceTrue()
	kc.Status.SetAddress(&apis.URL{
		Scheme: "http",
		Host:   names.ServiceHostName(svc.Name, svc.Namespace),
	})

	if kc.Status.GetCondition(v1alpha1.AzureChannelConditionStreamReady).IsUnknown() ||
		kc.Status.GetCondition(v1alpha1.AzureChannelConditionStreamReady).IsFalse() {
		creds, err := r.KubeClientSet.CoreV1().Secrets(kc.Namespace).Get(kc.Spec.EventHubName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		hubManager, _, err := r.hubClients(creds)
		if err != nil {
			return err
		}
		if err := r.setupAzureHub(ctx, kc.Spec.EventHubName, hubManager); err != nil {
			return err
		}
		ticker := time.NewTicker(time.Duration(3 * time.Minute))
		defer ticker.Stop()
		select {
		case <-ticker.C:
			ctx.Done()
			return fmt.Errorf("Stream didn't switch to active state in time")
		default:
			for {
				res, err := util.Describe(ctx, hubManager, kc.Spec.EventHubName)
				if err != nil {
					return err
				}

				if res == nil {
					return util.Create(ctx, hubManager, kc.Spec.EventHubName)
				}

				if *res.Status == eh.Active {
					break
				}

				time.Sleep(time.Second)
			}
		}
	}
	kc.Status.MarkStreamTrue()
	return nil
}

func (r *Reconciler) reconcileChannelService(ctx context.Context, channel *v1alpha1.AzureChannel) (*corev1.Service, error) {
	logger := logging.FromContext(ctx)
	// Get the  Service and propagate the status to the Channel in case it does not exist.
	// We don't do anything with the service because it's status contains nothing useful, so just do
	// an existence check. Then below we check the endpoints targeting it.
	// We may change this name later, so we have to ensure we use proper addressable when resolving these.
	svc, err := r.serviceLister.Services(channel.Namespace).Get(resources.MakeChannelServiceName(channel.Name))
	if err != nil {
		if apierrs.IsNotFound(err) {
			svc, err = resources.MakeK8sService(channel, resources.ExternalService(r.dispatcherNamespace, r.dispatcherServiceName))
			if err != nil {
				logger.Error("Failed to create the channel service object", zap.Error(err))
				return nil, err
			}
			svc, err = r.KubeClientSet.CoreV1().Services(channel.Namespace).Create(svc)
			if err != nil {
				logger.Error("Failed to create the channel service", zap.Error(err))
				return nil, err
			}
			return svc, nil
		}

		logger.Error("Unable to get the channel service", zap.Error(err))

		return nil, err
	}
	// Check to make sure that the AzureChannel owns this service and if not, complain.
	if !metav1.IsControlledBy(svc, channel) {
		return nil, fmt.Errorf("azurechannel: %s/%s does not own Service: %q", channel.Namespace, channel.Name, svc.Name)
	}
	return svc, nil
}

func (r *Reconciler) updateStatus(ctx context.Context, desired *v1alpha1.AzureChannel) (*v1alpha1.AzureChannel, error) {
	kc, err := r.azurechannelLister.AzureChannels(desired.Namespace).Get(desired.Name)
	if err != nil {
		return nil, err
	}

	if reflect.DeepEqual(kc.Status, desired.Status) {
		return kc, nil
	}

	becomesReady := desired.Status.IsReady() && !kc.Status.IsReady()

	// Don't modify the informers copy.
	existing := kc.DeepCopy()
	existing.Status = desired.Status

	new, err := r.AzureClientSet.MessagingV1alpha1().AzureChannels(desired.Namespace).UpdateStatus(existing)
	if err == nil && becomesReady {
		duration := time.Since(new.ObjectMeta.CreationTimestamp.Time)
		r.Logger.Infof("AzureChannel %q became ready after %v", kc.Name, duration)
		if err := r.StatsReporter.ReportReady("AzureChannel", kc.Namespace, kc.Name, duration); err != nil {
			r.Logger.Infof("Failed to record ready for AzureChannel %q: %v", kc.Name, err)
		}
	}
	return new, err
}

func (r *Reconciler) hubClients(creds *corev1.Secret) (*eventhub.HubManager, *eventhub.Hub, error) {
	if creds == nil {
		return nil, nil, fmt.Errorf("Credentials data is nil")
	}
	connStr, present := creds.Data["_connection_string"]
	if !present {
		return nil, nil, fmt.Errorf("\"_connection_string\" key is missing")
	}

	hubManager, err := eventhub.NewHubManagerFromConnectionString(string(connStr))
	if err != nil {
		return nil, nil, fmt.Errorf("Could not create new hub manager %v", err)
	}

	hub, err := eventhub.NewHubFromConnectionString(string(connStr))
	if err != nil {
		return nil, nil, fmt.Errorf("Could not create new hub %v", err)
	}

	return hubManager, hub, nil
}

func (r *Reconciler) setupAzureHub(ctx context.Context, hub string, hubManagerClient *eventhub.HubManager) error {
	if _, err := util.Describe(ctx, hubManagerClient, hub); err == nil {
		return nil
	}
	return util.Create(ctx, hubManagerClient, hub)
}

func (r *Reconciler) removeAzureHub(ctx context.Context, hub string, hubManagerClient *eventhub.HubManager) error {
	if _, err := util.Describe(ctx, hubManagerClient, hub); err != nil {
		return nil
	}
	return util.Delete(ctx, hubManagerClient, hub)
}
