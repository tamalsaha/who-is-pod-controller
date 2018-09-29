package main

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/meta"
	"path/filepath"
	"time"

	"github.com/appscode/go/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	_ "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/client-go/util/homedir"
)

type ObservableObject interface {
	GetResourceVersion() string
	GetGeneration() int64
	GetDeletionTimestamp() *metav1.Time
	GetLabels() map[string]string
	GetAnnotations() map[string]string
	//GetObservedGeneration() int64
	//GetObservedGenerationHash() string
}

func main() {
	masterURL := ""
	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
	if err != nil {
		log.Fatalf("Could not get Kubernetes config: %s", err)
	}
	err = rest.LoadTLSFiles(config)
	if err != nil {
		log.Fatalln(err)
	}
	kc := kubernetes.NewForConfigOrDie(config)

	hooks, err := kc.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().List(metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", "validators.kubedb.com").String(),
	})
	for _, h := range hooks.Items {
		fmt.Println(h.Name)
	}

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "validators.kubedb.com").String()
			return kc.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "validators.kubedb.com").String()
			return kc.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Watch(options)
		},
	}

	_, err = watchtools.ListWatchUntil(30*time.Second, lw,
		func(event watch.Event) (bool, error) {
			a, _ := meta.Accessor(event.Object)
			fmt.Println(event.Type, a.GetName())

			switch event.Type {
			case watch.Deleted:
				return false, nil
			case watch.Error:
				return false, fmt.Errorf("error watching")
			case watch.Added, watch.Modified:
				return true, nil
			default:
				return false, fmt.Errorf("unexpected event type: %v", event.Type)
			}
		})
	if err != nil {
		log.Fatalf("unable to get token for service account: %v", err)
	}
}
