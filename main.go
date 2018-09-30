package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/appscode/go/log"
	admreg_util "github.com/appscode/kutil/admissionregistration/v1beta1"
	watchtools "github.com/appscode/kutil/tools/watch"
	"k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
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
	"k8s.io/client-go/util/homedir"
)

func main() {
	masterURL := ""
	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
	if err != nil {
		log.Fatalf("Could not get Kubernetes config: %s", err)
	}
	err = UpdateValidatingWebhookCABundle(config, "validators.kubedb.com")
	if err != nil {
		log.Fatalf("unable to get token for service account: %v", err)
	}

	fmt.Println("DONE")
}

func UpdateValidatingWebhookCABundle(config *rest.Config, name string) error {
	err := rest.LoadTLSFiles(config)
	if err != nil {
		return err
	}

	kc := kubernetes.NewForConfigOrDie(config)

	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", name).String()
			return kc.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().List(options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", name).String()
			return kc.AdmissionregistrationV1beta1().ValidatingWebhookConfigurations().Watch(options)
		},
	}

	ctx := context.Background()
	_, err = watchtools.UntilWithSync(ctx,
		lw,
		&v1beta1.ValidatingWebhookConfiguration{},
		nil,
		func(event watch.Event) (bool, error) {
			a, _ := meta.Accessor(event.Object)
			fmt.Println(event.Type, a.GetName())

			switch event.Type {
			case watch.Deleted:
				return false, nil
			case watch.Error:
				return false, fmt.Errorf("error watching")
			case watch.Added, watch.Modified:
				cur := event.Object.(*v1beta1.ValidatingWebhookConfiguration)
				_, _, err := admreg_util.PatchValidatingWebhookConfiguration(kc, cur, func(in *v1beta1.ValidatingWebhookConfiguration) *v1beta1.ValidatingWebhookConfiguration {
					for i := range in.Webhooks {
						in.Webhooks[i].ClientConfig.CABundle = config.CAData
					}
					return in
				})
				return err == nil, err
			default:
				return false, fmt.Errorf("unexpected event type: %v", event.Type)
			}
		})
	return err
}
