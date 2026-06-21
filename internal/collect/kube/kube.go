// Package kube реализует collect.Reader поверх реального кластера через client-go (dynamic client).
// Для тестов/демо он подменяется на fsdir или SliceReader (инверсия зависимостей).
//
// Собираем cluster-wide ровно те виды, что нужны модели: Namespace, ServiceAccount,
// Deployment/StatefulSet/DaemonSet, Service, а также Istio AuthorizationPolicy и PeerAuthentication.
// Каждый объект сериализуется в JSON — нативный raw-формат, который понимает normalize.
package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/highluv/Go_analysis/internal/collect"
)

// Reader держит динамический клиент к кластеру.
type Reader struct {
	dyn dynamic.Interface
}

// NewInCluster — клиент из in-cluster конфигурации (когда acgd работает Pod'ом в кластере).
func NewInCluster() (*Reader, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return newFromConfig(cfg)
}

// NewFromKubeconfig — клиент из файла kubeconfig (локальный запуск против k3d).
func NewFromKubeconfig(path string) (*Reader, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig %s: %w", path, err)
	}
	return newFromConfig(cfg)
}

func newFromConfig(cfg *rest.Config) (*Reader, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return &Reader{dyn: dyn}, nil
}

// Перечень собираемых ресурсов. namespaced=false для cluster-scoped (Namespace).
var resources = []struct {
	gvr        schema.GroupVersionResource
	namespaced bool
}{
	{schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}, false},
	{schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true},
	{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true},
	{schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, true},
	{schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "authorizationpolicies"}, true},
	{schema.GroupVersionResource{Group: "security.istio.io", Version: "v1", Resource: "peerauthentications"}, true},
}

func (r *Reader) Read(ctx context.Context) ([]collect.RawManifest, error) {
	var out []collect.RawManifest
	for _, res := range resources {
		list, err := r.dyn.Resource(res.gvr).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			// Istio CRD может отсутствовать в кластере без Istio — это не фатально для сбора прочего.
			if res.gvr.Group == "security.istio.io" {
				continue
			}
			return nil, fmt.Errorf("list %s: %w", res.gvr.Resource, err)
		}
		for i := range list.Items {
			item := list.Items[i]
			raw, err := item.MarshalJSON()
			if err != nil {
				return nil, fmt.Errorf("marshal %s/%s: %w", item.GetNamespace(), item.GetName(), err)
			}
			out = append(out, collect.RawManifest{
				APIVersion: item.GetAPIVersion(),
				Kind:       item.GetKind(),
				Namespace:  item.GetNamespace(),
				Name:       item.GetName(),
				Raw:        raw,
			})
		}
	}
	return out, nil
}
