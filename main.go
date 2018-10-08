package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/appscode/go/log"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	masterURL := ""
	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
	if err != nil {
		log.Fatalf("Could not get Kubernetes config: %s", err)
	}

	w, err := DetectWorkload(config, v1.SchemeGroupVersion.WithResource("pods"), "voyager-operator-7f445b7c94-sc9vg", "kube-system")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(w.GetName())
}

func DetectWorkload(config *rest.Config, resource schema.GroupVersionResource, name, namespace string) (*unstructured.Unstructured, error) {
	kc := kubernetes.NewForConfigOrDie(config)
	dc, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	obj, err := dc.Resource(resource).Namespace(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return findWorkload(kc, dc, obj)
}

func findWorkload(kc kubernetes.Interface, dc dynamic.Interface, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	m, err := meta.Accessor(obj)
	if err != nil {
		return nil, err
	}
	for _, ref := range m.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller {
			gvk := schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind)
			gvr, err := ResourceForGVK(kc.Discovery(), gvk)
			if err != nil {
				return nil, err
			}
			parent, err := dc.Resource(gvr).Namespace(m.GetNamespace()).Get(ref.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			return findWorkload(kc, dc, parent)
		}
	}
	return obj, nil
}

func ResourceForGVK(client discovery.DiscoveryInterface, input schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	resourceList, err := client.ServerResourcesForGroupVersion(input.GroupVersion().String())
	if discovery.IsGroupDiscoveryFailedError(err) {
		glog.Errorf("Skipping failed API Groups: %v", err)
	} else if err != nil {
		return schema.GroupVersionResource{}, err
	}
	var resources []schema.GroupVersionResource
	for _, resource := range resourceList.APIResources {
		if resource.Kind == input.Kind { // match kind
			resources = append(resources, input.GroupVersion().WithResource(resource.Name))
		}
	}
	resources = FilterSubResources(resources) // ignore sub-resources
	if len(resources) == 1 {
		return resources[0], nil
	}
	return schema.GroupVersionResource{}, &AmbiguousResourceError{PartialResource: input, MatchingResources: resources}
}

func FilterSubResources(resources []schema.GroupVersionResource) []schema.GroupVersionResource {
	var resFiltered []schema.GroupVersionResource
	for _, res := range resources {
		if !strings.ContainsRune(res.Resource, '/') {
			resFiltered = append(resFiltered, res)
		}
	}
	return resFiltered
}

// AmbiguousResourceError is returned if the RESTMapper finds multiple matches for a resource
type AmbiguousResourceError struct {
	PartialResource schema.GroupVersionKind

	MatchingResources []schema.GroupVersionResource
	MatchingKinds     []schema.GroupVersionKind
}

func (e *AmbiguousResourceError) Error() string {
	switch {
	case len(e.MatchingKinds) > 0 && len(e.MatchingResources) > 0:
		return fmt.Sprintf("%v matches multiple resources %v and kinds %v", e.PartialResource, e.MatchingResources, e.MatchingKinds)
	case len(e.MatchingKinds) > 0:
		return fmt.Sprintf("%v matches multiple kinds %v", e.PartialResource, e.MatchingKinds)
	case len(e.MatchingResources) > 0:
		return fmt.Sprintf("%v matches multiple resources %v", e.PartialResource, e.MatchingResources)
	}
	return fmt.Sprintf("%v matches multiple resources or kinds", e.PartialResource)
}
