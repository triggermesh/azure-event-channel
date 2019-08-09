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
	"github.com/Azure/azure-event-hubs-go/v2"
	"go.uber.org/zap"

	mgmt "github.com/Azure/azure-sdk-for-go/services/eventhub/mgmt/2017-04-01/eventhub"
)

// Connect creates a new Azure Event Hub client
func Connect(subID string, logger *zap.SugaredLogger) *mgmt.EventHubsClient {
	client := mgmt.NewEventHubsClient(subID)
	return &client
}

//Describe describes specified event hub
func Describe(ctx context.Context, client mgmt.EventHubsClient, resGroupName, namespcName, eventHubName string) (mgmt.Model, error) {
	return client.Get(ctx, resGroupName, namespcName, eventHubName)
}

//Create creates or updates Event Hub with specified parameters
func Create(ctx context.Context, client mgmt.EventHubsClient, resGroupName, namespcName, eventHubName string) error {
	_, err := client.CreateOrUpdate(ctx, resGroupName, namespcName, eventHubName, mgmt.Model{})
	return err
}

//Delete removes Azure Event Hub with selected Name
func Delete(ctx context.Context, manager *eventhub.HubManager, azureEventHubName string) error {
	return manager.Delete(ctx, azureEventHubName)
}

// Publish publishes event to Azure Event Hub
func Publish(ctx context.Context, hub *eventhub.Hub, msg []byte, logger *zap.SugaredLogger) error {
	return hub.Send(ctx, eventhub.NewEvent(msg))
}
