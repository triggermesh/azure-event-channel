# Azure EventHub Knative Channel Controller

This Kubernetes controller provides Knative channels with [Azure Event Hub](https://docs.microsoft.com/en-us/azure/event-hubs/) as messaging backend.

Сreating `AzureChannel` object entails the following actions:

1. Controller connects to an Azure account with the correct credentials obtained from a secret 
2. Controller creates all the needed resources (`EventHub resource group`, `EventHub namespace`, `EventHub` and `SharedAccessPolicy` to connect to created Hub.) Note: all the titles are named after AzureChannel EventHubName spec property 
3. When connection is established and all resources are created, the dispatcher starts listening to Azure Event Hub and receive events. Once an event is received it is dispatched among subscribers (if any)
4. Post message to AzureChannel to send event to related Azure Event Hub.
5. When the AzureChannel is removed, the controller removes all the resources created in step 2 

## Deploy

We are using [ko](https://github.com/google/ko) tool to deploy custom resources:
```
ko apply -f config/
```
This will take all the configuration files and deploy the Azure CRD on your cluster into the `knative-eventing` namespace. Change the configurations in the `/config` directory before applying.

To verfiy that the controller is running, check the Pods:
```
kubectl -n knative-eventing get pods -l messaging.triggermesh.dev/channel=azure-channel
```

## Usage

In order for a channel to connect to your Azure account, you should create a k8s secret with `subscription_id`, `tenant_id`, `client_id` and `client_secret` keys. 

To obtain the Azure credentials use the [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli?view=azure-cli-latest) command: 

```
az ad sp create-for-rbac --sdk-auth > my.auth
```

Then create a secret in your namespace with the Azure access data from `my.auth` file. For example: 
```
kubectl create secret generic azure --from-literal=subscription_id="bd76c882-1234-406c-9574-3ab3c3f41b69" \
--from-literal=tenant_id="3cbc7d20-047b-1234-8b1c-63901c38f690" \
--from-literal=client_id="77b5cf16-64d9-1234-ae1f-2d4ca08b7dea" \
--from-literal=client_secret="19b44614-1234-41a7-b674-bb9df0e25764"
```

Now you are ready to create an `AzureChannel` object.
```
cat <<EOF | kubectl apply -f -
apiVersion: messaging.triggermesh.dev/v1alpha1
kind: AzureChannel
metadata:
  name: azure-test
spec:
  event_hub_name: "triggermesh"
  event_hub_region: "WEST US"
  secret_name: "azure"
EOF
```

Note that connecting to Azure and creating your event hub there takes some time (up to 1 minute).

As soon as the `azure-test` channel becomes ready you can subscribe to it with different services also described in `example` folder.

## Support

We would love your feedback and help on this project, so don't hesitate to let us know what is wrong and how we could improve them, just file an [issue](https://github.com/triggermesh/azure-event-channel/issues/new) or join those of use who are maintaining them and submit a [PR](https://github.com/triggermesh/azure-event-channel/compare).

## Code of conduct

This project is by no means part of [CNCF](https://www.cncf.io/) but we abide by its [code of conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md).



