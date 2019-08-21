/*
Copyright 2019 The Knative Authors

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

package v1alpha1

import (
	"github.com/knative/pkg/apis"
	"github.com/knative/pkg/apis/duck/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

var kc = apis.NewLivingConditionSet(
	AzureChannelConditionDispatcherReady,
	AzureChannelConditionServiceReady,
	AzureChannelConditionEndpointsReady,
	AzureChannelConditionHubReady,
	AzureChannelConditionAddressable,
	AzureChannelConditionChannelServiceReady)

const (
	// AzureChannelConditionReady has status True when all subconditions below have been set to True.
	AzureChannelConditionReady = apis.ConditionReady

	// AzureChannelConditionDispatcherReady has status True when a Dispatcher deployment is ready
	// Keyed off appsv1.DeploymentAvailable, which means minimum available replicas required are up
	// and running for at least minReadySeconds.
	AzureChannelConditionDispatcherReady apis.ConditionType = "DispatcherReady"

	// AzureChannelConditionServiceReady has status True when a k8s Service is ready. This
	// basically just means it exists because there's no meaningful status in Service. See Endpoints
	// below.
	AzureChannelConditionServiceReady apis.ConditionType = "ServiceReady"

	// AzureChannelConditionEndpointsReady has status True when a k8s Service Endpoints are backed
	// by at least one endpoint.
	AzureChannelConditionEndpointsReady apis.ConditionType = "EndpointsReady"

	AzureChannelConditionHubReady apis.ConditionType = "HubReady"

	// AzureChannelConditionAddressable has status true when this AzureChannel meets
	// the Addressable contract and has a non-empty hostname.
	AzureChannelConditionAddressable apis.ConditionType = "Addressable"

	// AzureChannelConditionChannelServiceReady has status True when a k8s Service representing the channel is ready.
	// Because this uses ExternalName, there are no endpoints to check.
	AzureChannelConditionChannelServiceReady apis.ConditionType = "ChannelServiceReady"
)

// GetCondition returns the condition currently associated with the given type, or nil.
func (cs *AzureChannelStatus) GetCondition(t apis.ConditionType) *apis.Condition {
	return kc.Manage(cs).GetCondition(t)
}

// IsReady returns true if the resource is ready overall.
func (cs *AzureChannelStatus) IsReady() bool {
	return kc.Manage(cs).IsHappy()
}

// InitializeConditions sets relevant unset conditions to Unknown state.
func (cs *AzureChannelStatus) InitializeConditions() {
	kc.Manage(cs).InitializeConditions()
}

// SetAddress sets the address (as part of Addressable contract) and marks the correct condition.
func (cs *AzureChannelStatus) SetAddress(url *apis.URL) {
	if cs.Address == nil {
		cs.Address = &v1alpha1.Addressable{}
	}
	if url != nil {
		cs.Address.Hostname = url.Host
		cs.Address.URL = url
		kc.Manage(cs).MarkTrue(AzureChannelConditionAddressable)
	} else {
		cs.Address.Hostname = ""
		cs.Address.URL = nil
		kc.Manage(cs).MarkFalse(AzureChannelConditionAddressable, "EmptyHostname", "hostname is the empty string")
	}
}

func (cs *AzureChannelStatus) MarkDispatcherFailed(reason, messageFormat string, messageA ...interface{}) {
	kc.Manage(cs).MarkFalse(AzureChannelConditionDispatcherReady, reason, messageFormat, messageA...)
}

// TODO: Unify this with the ones from Eventing. Say: Broker, Trigger.
func (cs *AzureChannelStatus) PropagateDispatcherStatus(ds *appsv1.DeploymentStatus) {
	for _, cond := range ds.Conditions {
		if cond.Type == appsv1.DeploymentAvailable {
			if cond.Status != corev1.ConditionTrue {
				cs.MarkDispatcherFailed("DispatcherNotReady", "Dispatcher Deployment is not ready: %s : %s", cond.Reason, cond.Message)
			} else {
				kc.Manage(cs).MarkTrue(AzureChannelConditionDispatcherReady)
			}
		}
	}
}

func (cs *AzureChannelStatus) MarkServiceFailed(reason, messageFormat string, messageA ...interface{}) {
	kc.Manage(cs).MarkFalse(AzureChannelConditionServiceReady, reason, messageFormat, messageA...)
}

func (cs *AzureChannelStatus) MarkServiceTrue() {
	kc.Manage(cs).MarkTrue(AzureChannelConditionServiceReady)
}

func (cs *AzureChannelStatus) MarkChannelServiceFailed(reason, messageFormat string, messageA ...interface{}) {
	kc.Manage(cs).MarkFalse(AzureChannelConditionChannelServiceReady, reason, messageFormat, messageA...)
}

func (cs *AzureChannelStatus) MarkChannelServiceTrue() {
	kc.Manage(cs).MarkTrue(AzureChannelConditionChannelServiceReady)
}

func (cs *AzureChannelStatus) MarkEndpointsFailed(reason, messageFormat string, messageA ...interface{}) {
	kc.Manage(cs).MarkFalse(AzureChannelConditionEndpointsReady, reason, messageFormat, messageA...)
}

func (cs *AzureChannelStatus) MarkEndpointsTrue() {
	kc.Manage(cs).MarkTrue(AzureChannelConditionEndpointsReady)
}

func (cs *AzureChannelStatus) MarkHubFailed(reason, messageFormat string, messageA ...interface{}) {
	kc.Manage(cs).MarkFalse(AzureChannelConditionHubReady, reason, messageFormat, messageA...)
}

func (cs *AzureChannelStatus) MarkHubTrue() {
	kc.Manage(cs).MarkTrue(AzureChannelConditionHubReady)
}
