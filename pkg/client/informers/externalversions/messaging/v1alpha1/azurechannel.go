/*
Copyright (c) 2020 TriggerMesh Inc.

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
// Code generated by informer-gen. DO NOT EDIT.

package v1alpha1

import (
	time "time"

	messagingv1alpha1 "github.com/triggermesh/azure-event-channel/pkg/apis/messaging/v1alpha1"
	internalclientset "github.com/triggermesh/azure-event-channel/pkg/client/clientset/internalclientset"
	internalinterfaces "github.com/triggermesh/azure-event-channel/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "github.com/triggermesh/azure-event-channel/pkg/client/listers/messaging/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// AzureChannelInformer provides access to a shared informer and lister for
// AzureChannels.
type AzureChannelInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.AzureChannelLister
}

type azureChannelInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewAzureChannelInformer constructs a new informer for AzureChannel type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewAzureChannelInformer(client internalclientset.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredAzureChannelInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredAzureChannelInformer constructs a new informer for AzureChannel type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredAzureChannelInformer(client internalclientset.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MessagingV1alpha1().AzureChannels(namespace).List(options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.MessagingV1alpha1().AzureChannels(namespace).Watch(options)
			},
		},
		&messagingv1alpha1.AzureChannel{},
		resyncPeriod,
		indexers,
	)
}

func (f *azureChannelInformer) defaultInformer(client internalclientset.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredAzureChannelInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *azureChannelInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&messagingv1alpha1.AzureChannel{}, f.defaultInformer)
}

func (f *azureChannelInformer) Lister() v1alpha1.AzureChannelLister {
	return v1alpha1.NewAzureChannelLister(f.Informer().GetIndexer())
}
