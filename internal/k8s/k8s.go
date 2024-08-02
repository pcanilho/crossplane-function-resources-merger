// Package k8s provides an interface to interact with Kubernetes resources.
package k8s

import (
	"context"
	"os"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Option is a functional option for the Controller.
type Option = func(*Controller)

// Controller is a Kubernetes controller.
type Controller struct {
	client dynamic.Interface
	mapper *restmapper.DeferredDiscoveryRESTMapper
	ctx    context.Context

	Timeout time.Duration
}

// WithTimeout sets the timeout for the controller.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Controller) {
		c.Timeout = timeout
	}
}

// NewController creates a new Kubernetes controller.
func NewController(opts ...Option) (*Controller, error) {
	_inst := new(Controller)
	for _, opt := range opts {
		opt(_inst)
	}

	cfg, err := getKubeConfig()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get kubeconfig")
	}

	cfg.Timeout = _inst.Timeout
	clt, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create dynamic client")
	}

	dc, _ := discovery.NewDiscoveryClientForConfig(cfg)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	_inst.mapper = mapper
	_inst.client = clt
	_inst.ctx = context.Background()
	return _inst, nil
}

// GetResource gets a resource from the Kubernetes cluster.
func (c *Controller) GetResource(ctx context.Context, namespace, name string, resource schema.GroupVersionKind, opts metav1.GetOptions) (*unstructured.Unstructured, error) {
	if ctx == nil {
		ctx = c.ctx
	}

	mapping, err := c.mapper.RESTMapping(schema.GroupKind{
		Group: resource.Group,
		Kind:  resource.Kind,
	}, resource.Version)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get REST mapping")
	}

	client := c.client.Resource(mapping.Resource)
	namespacedClient := client.Namespace(namespace)
	res, err := namespacedClient.Get(ctx, name, opts)
	if err != nil {
		res, err = client.Get(ctx, name, opts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get resource")
		}
	}
	return res, nil
}

func getKubeConfig() (config *rest.Config, err error) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != "" {
		// in-cluster config
		return rest.InClusterConfig()
	}
	// out-of-cluster config
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return clientConfig.ClientConfig()
}
