# Azure Knative Custom Controller

Cluster channel provisioner provides Knative channels with [Azure Event Hub](https://docs.microsoft.com/en-us/azure/event-hubs/) as message queue backend.

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

## Run localy 

To run the controller:
```
export SYSTEM_NAMESPACE=default
```

``` 
go run cmd/channel_controller/main.go -kubeconfig="$HOME/.kube/config" -hardCodedLoggingConfig=true
```
