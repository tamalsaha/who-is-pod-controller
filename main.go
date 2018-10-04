package main

import (
	"encoding/json"
	"github.com/evanphx/json-patch"
	api "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	"bytes"
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"path/filepath"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	"github.com/appscode/go/log"
	admreg_util "github.com/appscode/kutil/admissionregistration/v1beta1"
	"github.com/appscode/kutil/discovery"
	watchtools "github.com/appscode/kutil/tools/watch"
	"k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	d := Detector{
		config: config,
		obj: &api.Postgres{
			TypeMeta: metav1.TypeMeta{
				APIVersion: api.SchemeGroupVersion.String(),
				Kind:       api.ResourceKindPostgres,
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "webhook-activation-detection",
			},
			Spec: api.PostgresSpec{
				Version: "9.6-v1",
				TerminationPolicy: api.TerminationPolicyWipeOut,


			},
		},
		op: v1beta1.Create,
	}
	d.check()

	fmt.Println("DONE")
}

/*
Create -> FAIL

Create , PATCH, DELETE


*/

type Detector struct {
	config *rest.Config
	obj runtime.Object
	op v1beta1.OperationType
	transform func(_ runtime.Object)
}

func (d Detector) check() error {
	kc := kubernetes.NewForConfigOrDie(d.config)
	dc, _ := dynamic.NewForConfig(d.config)

	gvk := d.obj.GetObjectKind().GroupVersionKind()
	fmt.Println("GVK = ", gvk)

	gvr, _ := discovery.ResourceForGVK(kc.Discovery(), gvk)
	fmt.Println("GVR = ", gvr)

	accessor, _ := meta.Accessor(d.obj)

	var ri dynamic.ResourceInterface

	if accessor.GetNamespace() != "" {
		ri = dc.Resource(gvr).Namespace(accessor.GetNamespace())
	} else {
		dc.Resource(gvr)
	}

	var buf bytes.Buffer
	unstructured.UnstructuredJSONScheme.Encode(d.obj, &buf)

	u := unstructured.Unstructured{}
	unstructured.UnstructuredJSONScheme.Decode(buf.Bytes(), nil, &u)

	if d.op == v1beta1.Create {
		_, err := ri.Create(&u)
		fmt.Println(err)

		if kerr.IsForbidden(err) {
			// admission webhook "postgres.validators.kubedb.com" denied the request: spec.replicas "<nil>" invalid. Value must be greater than zero
			fmt.Println(kerr.ReasonForError(err))
			fmt.Println(err.Error())
		} else if err == nil {
			_ = ri.Delete(accessor.GetName(), &metav1.DeleteOptions{})
		} else {
			// some other error
		}

	} else if d.op == v1beta1.Update {
		_, err := ri.Create(&u)
		fmt.Println(err)

		mod := d.obj.DeepCopyObject()
		d.transform(mod)
		modJson, err := json.Marshal(mod)

		patch, err := jsonpatch.CreateMergePatch(buf.Bytes(), modJson)
		if err != nil {
			return err

		}

		_, err = ri.Patch(accessor.GetName(), types.MergePatchType, patch)

		if kerr.IsForbidden(err) {
			// admission webhook "postgres.validators.kubedb.com" denied the request: spec.replicas "<nil>" invalid. Value must be greater than zero
			fmt.Println(kerr.ReasonForError(err))
			fmt.Println(err.Error())
		} else if err == nil {
		} else {
			// some other error
		}

		_ = ri.Delete(accessor.GetName(), &metav1.DeleteOptions{})
	} else if d.op == v1beta1.Delete {
		_, err := ri.Create(&u)
		fmt.Println(err)

		err = ri.Delete(accessor.GetName(), &metav1.DeleteOptions{})
		if kerr.IsForbidden(err) {
			// admission webhook "postgres.validators.kubedb.com" denied the request: spec.replicas "<nil>" invalid. Value must be greater than zero
			fmt.Println(kerr.ReasonForError(err))
			fmt.Println(err.Error())
		} else if err == nil {
		} else {
			// some other error
		}
	}

	return nil
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
