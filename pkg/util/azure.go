package util

// import (
// 	"context"
// 	"fmt"
// 	"log"
// 	"time"

// 	eventhubs "github.com/Azure/azure-event-hubs-go"
// 	"github.com/Azure/azure-event-hubs-go/persist"

// 	"github.com/Azure/azure-sdk-for-go/services/eventhub/mgmt/2017-04-01/eventhub"
// 	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2017-05-10/resources"
// 	"github.com/Azure/go-autorest/autorest/azure"
// 	"github.com/Azure/go-autorest/autorest/azure/auth"
// 	"github.com/Azure/go-autorest/autorest/to"
// )

// func main() {
// 	hubClient, err := CreateEventHubClient(context.Background(), "triggermesh", "West US")
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	fmt.Println("Connected to new hub: ", hubClient)

// 	go receive(context.Background(), hubClient)

// 	time.Sleep(5 * time.Minute)

// 	log.Fatal(DeleteResources(context.Background(), "triggermesh"))
// }

// // CreateEventHubClient creates an Event Hub client
// func CreateEventHubClient(ctx context.Context, resourceName, location string) (*eventhubs.Hub, error) {
// 	settings, err := auth.GetSettingsFromFile()
// 	if err != nil {
// 		return nil, err
// 	}

// 	namespaceClient := eventhub.NewNamespacesClient(settings.GetSubscriptionID())
// 	groupsClient := resources.NewGroupsClient(settings.GetSubscriptionID())
// 	hubClient := eventhub.NewEventHubsClient(settings.GetSubscriptionID())

// 	// create an authorizer from env vars or Azure Managed Service Idenity
// 	authorizer, err := auth.NewAuthorizerFromFile(azure.PublicCloud.ResourceManagerEndpoint)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	namespaceClient.Authorizer = authorizer
// 	groupsClient.Authorizer = authorizer
// 	hubClient.Authorizer = authorizer

// 	resourceGroup, err := groupsClient.CreateOrUpdate(
// 		ctx,
// 		resourceName,
// 		resources.Group{
// 			Location: to.StringPtr(location),
// 		})

// 	if err != nil {
// 		return nil, err
// 	}

// 	future, err := namespaceClient.CreateOrUpdate(
// 		ctx,
// 		*resourceGroup.Name,
// 		resourceName,
// 		eventhub.EHNamespace{
// 			Location: to.StringPtr(location),
// 		},
// 	)

// 	if err != nil {
// 		return nil, err
// 	}

// 	err = future.WaitForCompletionRef(ctx, namespaceClient.Client)
// 	if err != nil {
// 		return nil, err
// 	}

// 	namespace, err := future.Result(namespaceClient)
// 	if err != nil {
// 		return nil, err
// 	}

// 	ehModel, err := hubClient.CreateOrUpdate(
// 		ctx,
// 		*resourceGroup.Name,
// 		*namespace.Name,
// 		resourceName,
// 		eventhub.Model{
// 			Properties: &eventhub.Properties{
// 				PartitionCount: to.Int64Ptr(4),
// 			},
// 		},
// 	)

// 	if err != nil {
// 		return nil, err
// 	}

// 	authRule := eventhub.AuthorizationRule{}
// 	authRule.AuthorizationRuleProperties = &eventhub.AuthorizationRuleProperties{
// 		Rights: &[]eventhub.AccessRights{
// 			eventhub.Listen,
// 			eventhub.Manage,
// 			eventhub.Send,
// 		},
// 	}

// 	rule, err := hubClient.CreateOrUpdateAuthorizationRule(
// 		ctx,
// 		*resourceGroup.Name,
// 		*namespace.Name,
// 		*ehModel.Name,
// 		resourceName,
// 		authRule,
// 	)

// 	if err != nil {
// 		return nil, err
// 	}

// 	accessKeys, err := hubClient.ListKeys(
// 		ctx,
// 		*resourceGroup.Name,
// 		*namespace.Name,
// 		*ehModel.Name,
// 		*rule.Name,
// 	)

// 	if err != nil {
// 		return nil, err
// 	}

// 	fmt.Println("Connection string for new event hub: ", *accessKeys.PrimaryConnectionString)

// 	return eventhubs.NewHubFromConnectionString(*accessKeys.PrimaryConnectionString)

// }

// // DeleteResources deletes an Event Hubs resources
// func DeleteResources(ctx context.Context, resourceName string) error {
// 	settings, err := auth.GetSettingsFromFile()
// 	if err != nil {
// 		return err
// 	}

// 	groupsClient := resources.NewGroupsClient(settings.GetSubscriptionID())

// 	// create an authorizer from env vars or Azure Managed Service Idenity
// 	authorizer, err := auth.NewAuthorizerFromFile(azure.PublicCloud.ResourceManagerEndpoint)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	groupsClient.Authorizer = authorizer

// 	future, err := groupsClient.Delete(ctx, resourceName)

// 	if err != nil {
// 		return err
// 	}

// 	err = future.WaitForCompletionRef(ctx, groupsClient.Client)
// 	if err != nil {
// 		return err
// 	}

// 	_, err = future.Result(groupsClient)
// 	return err
// }

// func receive(ctx context.Context, hub *eventhubs.Hub) {

// 	// get info about the hub, particularly number and IDs of partitions
// 	info, err := hub.GetRuntimeInformation(ctx)
// 	if err != nil {
// 		log.Fatalf("failed to get runtime info: %s\n", err)
// 	}
// 	log.Printf("partition IDs: %s\n", info.PartitionIDs)

// 	// set up wait group to wait for expected message
// 	eventReceived := make(chan struct{})

// 	// declare handler for incoming events
// 	handler := func(ctx context.Context, event *eventhubs.Event) error {
// 		fmt.Printf("received: %s\n", string(event.Data))
// 		// notify channel that event was received
// 		eventReceived <- struct{}{}
// 		return nil
// 	}

// 	for _, partitionID := range info.PartitionIDs {
// 		_, err := hub.Receive(
// 			ctx,
// 			partitionID,
// 			handler,
// 			eventhubs.ReceiveWithStartingOffset(persist.StartOfStream),
// 		)
// 		if err != nil {
// 			log.Fatalf("failed to receive for partition ID %s: %s\n", partitionID, err)
// 		}
// 	}

// 	// don't exit till event is received by handler
// 	select {
// 	case <-eventReceived:
// 	case err := <-ctx.Done():
// 		log.Fatalf("context cancelled before event received: %s\n", err)
// 	}
// }
