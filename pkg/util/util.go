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

package util

import (
	"context"
	"github.com/Azure/azure-event-hubs-go"
)

//Describe describes specified event hub
func Describe(ctx context.Context, client *eventhub.HubManager, eventHubName string) (*eventhub.HubEntity, error) {
	return client.Get(ctx, eventHubName)
}

//Delete removes Azure Event Hub with selected Name
func Delete(ctx context.Context, client *eventhub.HubManager, eventHubName string) error {
	return client.Delete(ctx, eventHubName)
}

//Create creates Azure Event Hub with selected Name
func Create(ctx context.Context, client *eventhub.HubManager, eventHubName string) error {
	_, err := client.Put(ctx, eventHubName)
	return err
}