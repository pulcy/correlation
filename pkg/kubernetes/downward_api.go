package kubernetes

import (
	"io/ioutil"
	"strings"
)

const (
	namespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// GetKubernetesNamespace loads the namespace of the pod running this process.
// If the namespace is not found, (e.g. because the process is not running in kubernetes)
// an empty string is returned.
func GetKubernetesNamespace() string {
	raw, err := ioutil.ReadFile(namespacePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
