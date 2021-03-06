package client

import "github.com/YakLabs/k8s-client/intstr"

type (
	// IngressInterface has methods to work with Ingress resources.
	IngressInterface interface {
		CreateIngress(namespace string, item *Ingress) (*Ingress, error)
		GetIngress(namespace, name string) (result *Ingress, err error)
		ListIngresses(namespace string, opts *ListOptions) (*IngressList, error)
		DeleteIngress(namespace, name string) error
		UpdateIngress(namespace string, item *Ingress) (*Ingress, error)
	}

	// Ingress holds secret data of a certain type.
	Ingress struct {
		TypeMeta   `json:",inline"`
		ObjectMeta `json:"metadata,omitempty"`

		// Spec is the desired state of the Ingress.
		Spec *IngressSpec `json:"spec,omitempty"`
		// Status is the current state of the Ingress.
		Status *IngressStatus `json:"status,omitempty"`
	}

	// IngressList holds a list of ingresses.
	IngressList struct {
		TypeMeta `json:",inline"`
		ListMeta `json:"metadata,omitempty"`

		Items []Ingress `json:"items"`
	}

	// IngressSpec describes the Ingress the user wishes to exist.
	IngressSpec struct {
		// A default backend capable of servicing requests that don’t match any rule.
		// At least one of backend or rules must be specified.
		// This field is optional to allow the loadbalancer controller or defaulting logic to specify a global default.
		Backend *IngressBackend `json:"backend,omitempty"`

		// TLS configuration.
		// Currently the Ingress only supports a single TLS port, 443.
		// If multiple members of this list specify different hosts, they will be multiplexed on the same port according to the hostname
		// specified through the SNI TLS extension, if the ingress controller fulfilling the ingress supports SNI.
		TLS []IngressTLS `json:"tls,omitempty"`

		// A list of host rules used to configure the Ingress.
		// If unspecified, or no rule matches, all traffic is sent to the default backend.
		Rules []IngressRule `json:"rules,omitempty"`
	}

	// IngressBackend describes all endpoints for a given service and port.
	IngressBackend struct {
		//		Specifies the name of the referenced service.
		ServiceName string `json:"serviceName"`

		// Specifies the port of the referenced service.
		ServicePort intstr.IntOrString `json:"servicePort"`
	}

	// IngressTLS describes the transport layer security associated with an Ingress.
	IngressTLS struct {
		// Hosts are a list of hosts included in the TLS certificate.
		// The values in this list must match the name/s used in the tlsSecret.
		// Defaults to the wildcard host setting for the loadbalancer controller fulfilling this Ingress, if left unspecified.
		Hosts []string `json:"hosts,omitempy"`

		// SecretName is the name of the secret used to terminate SSL traffic on 443.
		// Field is left optional to allow SSL routing based on SNI hostname alone.
		// If the SNI host in a listener conflicts with the "Host" header field used by an IngressRule,
		// the SNI host is used for termination and value of the Host header is used for routing.
		SecretName string `json:"secretName,omitempty"`
	}

	// IngressRule represents the rules mapping the paths under a specified host to the related backend services.
	// Incoming requests are first evaluated for a host match, then routed to the backend associated with the matching IngressRuleValue.
	IngressRule struct {
		// Host is the fully qualified domain name of a network host, as defined by RFC 3986.
		// Note the following deviations from the "host" part of the URI as defined in the RFC:
		// 1. IPs are not allowed. Currently an IngressRuleValue can only apply to the IP in the Spec of the parent Ingress.
		// 2. The : delimiter is not respected because ports are not allowed.
		// Currently the port of an Ingress is implicitly :80 for http and :443 for https.
		// Both these may change in the future. Incoming requests are matched against the host before the IngressRuleValue.
		// If the host is unspecified, the Ingress routes all traffic based on the specified IngressRuleValue.
		Host string `json:"host,omitempty"`

		HTTP *HTTPIngressRuleValue `json:"http,omitempty"`
	}

	// HTTPIngressRuleValue is a list of http selectors pointing to backends.
	// In the example: http://<host>/<path>?<searchpart> → backend where where parts of the url correspond to RFC 3986,
	// this resource will be used to match against everything after the last / and before the first ? or #.
	HTTPIngressRuleValue struct {
		// A collection of paths that map requests to backends
		Paths []HTTPIngressPath `json:"paths,omitempty"`
	}

	// HTTPIngressPath associates a path regex with a backend.
	// Incoming urls matching the path are forwarded to the backend.
	HTTPIngressPath struct {
		// Path is an extended POSIX regex as defined by IEEE Std 1003.1, (i.e this follows the egrep/unix syntax,
		// not the perl syntax) matched against the path of an incoming request.
		// Currently it can contain characters disallowed from the conventional "path" part of a URL as defined by RFC 3986.
		// Paths must begin with a /. If unspecified, the path defaults to a catch all sending traffic to the backend.
		Path string `json:"path,omitempty"`

		// Backend defines the referenced service endpoint to which the traffic will be forwarded to.
		Backend IngressBackend `json:"backend"`
	}

	// IngressStatus describe the current state of the Ingress.
	IngressStatus struct {
		// LoadBalancer contains the current status of the load-balancer.
		LoadBalancer *LoadBalancerStatus `json:"loadBalancer,omitempty"`
	}
)

// NewIngress creates a new Ingress struct
func NewIngress(namespace, name string) *Ingress {
	return &Ingress{
		TypeMeta:   NewTypeMeta("Ingress", "extensions/v1beta1"),
		ObjectMeta: NewObjectMeta(namespace, name),
		Spec:       &IngressSpec{},
	}
}
