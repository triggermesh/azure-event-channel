# Azure Knative Custom Controller

Cluster channel provisioner provides Knative channels with [Azure Event Hub](https://docs.microsoft.com/en-us/azure/event-hubs/) as messaging backend.

## Generating Code for Custom Controller 
In case of any changes to types for the custom controller, use the following commands to regenerate client and deepcopy files

```
ROOT_PACKAGE="github.com/triggermesh/azure-event-channel"
CUSTOM_RESOURCE_NAME="messaging"
CUSTOM_RESOURCE_VERSION="v1alpha1"

go get -u k8s.io/code-generator/...
cd $GOPATH/src/k8s.io/code-generator

./generate-groups.sh all "$ROOT_PACKAGE/pkg/client" "$ROOT_PACKAGE/pkg/apis" "$CUSTOM_RESOURCE_NAME:$CUSTOM_RESOURCE_VERSION"
```

## Deploy

To deploy controller and dispatcher use 
```
ko apply -f config/
```
This will take all the configurations and deploy Azure CRD on your cluster to `knative-eventing` namespace. Change configurations if needed.

To see it's running use 
```
kubectl -n knative-eventing get pods
```
To follow logs use 
```
kubectl logs --tail=50 <name of your pod here> -f 
```

## Usage

First create a secret with your Azure access data in your namespace. 
Example: 
```
kubectl -n yourNamespace create secret generic azure --from-literal=subscription_id="bd76c882-1234-406c-9574-3ab3c3f41b69" --from-literal=tenant_id="3cbc7d20-047b-1234-8b1c-63901c38f690" --from-literal=client_id="77b5cf16-64d9-1234-ae1f-2d4ca08b7dea" --from-literal=client_secret="19b44614-1234-41a7-b674-bb9df0e25764"
```

In order for a channel to connect to your Azure account, it expects the following keys in the secret: 
`subscription_id`, `tenant_id`, `client_id`, `client_secret` 

To obtain credentials use [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli?view=azure-cli-latest) command: 
```
az ad sp create-for-rbac --sdk-auth > my.auth
```

Then create Azure Channel
```
kubectl -n yourNamespace create -f example/azure-channel-example.yaml
```

Note that connecting to Azure and creating your event hub there takes some time. (about 40sec - 1 minute)

As soon as azure-test channel becomes ready you can subscribe to it with different services also described in example folder


