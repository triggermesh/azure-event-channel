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

package v1alpha1

import (
	"context"
	"fmt"

	"knative.dev/pkg/apis"
)

func (c *AzureChannel) Validate(ctx context.Context) *apis.FieldError {
	return c.Spec.Validate(ctx).ViaField("spec")
}

func (cs *AzureChannelSpec) Validate(ctx context.Context) *apis.FieldError {
	var errs *apis.FieldError

	for i, subscriber := range cs.Subscribers {
		if subscriber.ReplyURI == nil && subscriber.SubscriberURI == nil {
			fe := apis.ErrMissingField("replyURI", "subscriberURI")
			fe.Details = "expected at least one of, got none"
			errs = errs.Also(fe.ViaField(fmt.Sprintf("subscriber[%d]", i)).ViaField("subscribable"))
		}
	}

	if cs.EventHubName == "" {
		fe := apis.ErrMissingField("event_hub_name")
		fe.Details = "expected event hub name, got none"
		errs = errs.Also(fe)
	}

	if cs.SecretName == "" {
		fe := apis.ErrMissingField("secret_name")
		fe.Details = "expected secret name, got none"
		errs = errs.Also(fe)
	}

	if cs.EventHubRegion == "" {
		fe := apis.ErrMissingField("secret_name")
		fe.Details = "expected event hub region, got none"
		errs = errs.Also(fe)
	}

	return errs
}
