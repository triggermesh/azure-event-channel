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

	"github.com/Azure/go-autorest/autorest/to"

	eventhubs "github.com/Azure/azure-event-hubs-go"
	"github.com/Azure/azure-sdk-for-go/services/eventhub/mgmt/2017-04-01/eventhub"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2017-05-10/resources"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
)

type AzureEventHubClient struct {
	NamespaceClient eventhub.NamespacesClient
	GroupClient     resources.GroupsClient
	HubClient       eventhub.EventHubsClient
	Hub             *eventhubs.Hub
}

//Connect establishes connection with Azure
func Connect(ctx context.Context, subscriptionID, tenantID, clientID, clientSecret string) (AzureEventHubClient, error) {

	settings := auth.EnvironmentSettings{
		Values: map[string]string{
			"AZURE_SUBSCRIPTION_ID":   subscriptionID,
			"AZURE_TENANT_ID":         tenantID,
			"AZURE_CLIENT_ID":         clientID,
			"AZURE_CLIENT_SECRET":     clientSecret,
			"GalleryEndpoint":         azure.PublicCloud.GalleryEndpoint,
			"ManagementEndpoint":      azure.PublicCloud.ServiceManagementEndpoint,
			"AZURE_AD_RESOURCE":       azure.PublicCloud.ResourceManagerEndpoint,
			"ActiveDirectoryEndpoint": azure.PublicCloud.ActiveDirectoryEndpoint,
			"ResourceManagerEndpoint": azure.PublicCloud.ResourceManagerEndpoint,
			"GraphResourceID":         azure.PublicCloud.GraphEndpoint,
			"SQLManagementEndpoint":   "https://management.core.windows.net:8443/",
		},
		Environment: azure.PublicCloud,
	}

	namespaceClient := eventhub.NewNamespacesClient(settings.GetSubscriptionID())
	groupsClient := resources.NewGroupsClient(settings.GetSubscriptionID())
	hubClient := eventhub.NewEventHubsClient(settings.GetSubscriptionID())

	// create an authorizer from env vars or Azure Managed Service Idenity
	authorizer, err := settings.GetAuthorizer()
	if err != nil {
		return AzureEventHubClient{}, err
	}

	namespaceClient.Authorizer = authorizer
	groupsClient.Authorizer = authorizer
	hubClient.Authorizer = authorizer

	return AzureEventHubClient{namespaceClient, groupsClient, hubClient, nil}, nil
}

// CreateOrUpdateHub creates or updates hub with provided name and an Azure region
// It creates group, namespace and then hub and auth rule to connect to it.
func (conn AzureEventHubClient) CreateOrUpdateHub(ctx context.Context, name, region string) (*eventhubs.Hub, error) {

	resourceGroup, err := conn.GroupClient.CreateOrUpdate(
		ctx,
		name,
		resources.Group{
			Location: to.StringPtr(region),
		})

	if err != nil {
		return nil, err
	}

	future, err := conn.NamespaceClient.CreateOrUpdate(
		ctx,
		*resourceGroup.Name,
		name,
		eventhub.EHNamespace{
			Location: to.StringPtr(region),
		},
	)

	if err != nil {
		return nil, err
	}

	err = future.WaitForCompletionRef(ctx, conn.NamespaceClient.Client)
	if err != nil {
		return nil, err
	}

	namespace, err := future.Result(conn.NamespaceClient)
	if err != nil {
		return nil, err
	}

	ehModel, err := conn.HubClient.CreateOrUpdate(
		ctx,
		*resourceGroup.Name,
		*namespace.Name,
		name,
		eventhub.Model{
			Properties: &eventhub.Properties{
				PartitionCount: to.Int64Ptr(4),
			},
		},
	)

	if err != nil {
		return nil, err
	}

	authRule := eventhub.AuthorizationRule{}
	authRule.AuthorizationRuleProperties = &eventhub.AuthorizationRuleProperties{
		Rights: &[]eventhub.AccessRights{
			eventhub.Listen,
			eventhub.Manage,
			eventhub.Send,
		},
	}

	rule, err := conn.HubClient.CreateOrUpdateAuthorizationRule(
		ctx,
		*resourceGroup.Name,
		*namespace.Name,
		*ehModel.Name,
		name,
		authRule,
	)

	if err != nil {
		return nil, err
	}

	accessKeys, err := conn.HubClient.ListKeys(
		ctx,
		*resourceGroup.Name,
		*namespace.Name,
		*ehModel.Name,
		*rule.Name,
	)

	if err != nil {
		return nil, err
	}

	hub, err := eventhubs.NewHubFromConnectionString(*accessKeys.PrimaryConnectionString)
	if err != nil {
		return nil, err
	}

	conn.Hub = hub

	return hub, nil
}

// DeleteHub deletes whole group that results in deletion of hub, namespace and group
// this is implemented for consistency reasons. Deletion usually takes sometime to complete
func (conn AzureEventHubClient) DeleteHub(ctx context.Context, name string) error {
	future, err := conn.GroupClient.Delete(ctx, name)

	if err != nil {
		return err
	}

	err = future.WaitForCompletionRef(ctx, conn.GroupClient.Client)
	if err != nil {
		return err
	}

	_, err = future.Result(conn.GroupClient)
	return err
}
