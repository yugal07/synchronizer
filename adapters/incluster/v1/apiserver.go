package incluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"

	clouds "github.com/kubescape/k8s-interface/cloudsupport"
	"github.com/kubescape/k8s-interface/k8sinterface"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// azureResourceGroupMarker is the path segment that precedes the resource
// group name inside an Azure-style Kubernetes node providerID, e.g.
// azure:///subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Compute/...
const azureResourceGroupMarker = "/resourceGroups/"

func GetApiServerGitVersionAndCloudProvider(ctx context.Context, k8sApi *k8sinterface.KubernetesApi) (string, string) {
	if k8sApi == nil {
		logger.L().Warning("no kubernetes client for api server info")
		return "", ""
	}

	cloudProvider, err := getCloudProvider(ctx, k8sApi)
	if err != nil {
		logger.L().Error("failed to set cloud provider", helpers.Error(err))
	} else {
		logger.L().Info("cloud provider", helpers.String("cloudProvider", cloudProvider))
	}

	gitVersion, err := getApiServerGitVersion(k8sApi)
	if err != nil {
		logger.L().Error("failed to get api server version", helpers.Error(err))
	} else {
		logger.L().Info("cluster api server", helpers.String("GitVersion", gitVersion))
	}

	return gitVersion, cloudProvider
}

// GetClusterUID retrieves the UID of the kube-system namespace to use as a stable cluster identifier.
func GetClusterUID(ctx context.Context, k8sApi *k8sinterface.KubernetesApi) string {
	if k8sApi == nil {
		logger.L().Ctx(ctx).Warning("no kubernetes client for ClusterUID")
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	namespace, err := k8sApi.KubernetesClient.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err != nil {
		logger.L().Ctx(ctx).Warning("failed to get kube-system namespace for ClusterUID", helpers.Error(err))
		return ""
	}
	clusterUID := string(namespace.UID)
	logger.L().Info("successfully retrieved ClusterUID", helpers.String("clusterUID", clusterUID))
	return clusterUID
}

func getCloudProvider(ctx context.Context, k8sApi *k8sinterface.KubernetesApi) (string, error) {
	// Fetch only a single node to extract the providerID, instead of listing all nodes.
	nodeList, err := k8sApi.KubernetesClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(nodeList.Items) == 0 {
		return "", fmt.Errorf("no nodes found in the cluster")
	}
	return clouds.GetCloudProvider(nodeList), nil
}

// GetResourceGroup returns the Azure resource group hosting the cluster by
// parsing the providerID of any node (all nodes of an AKS cluster share the
// same resource group). Returns an empty string for non-Azure clusters or if
// the information cannot be determined; callers should treat emptiness as
// "unknown" rather than an error.
func GetResourceGroup(ctx context.Context, k8sApi *k8sinterface.KubernetesApi) string {
	if k8sApi == nil {
		logger.L().Ctx(ctx).Warning("no kubernetes client for ResourceGroup")
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	nodeList, err := k8sApi.KubernetesClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		logger.L().Ctx(ctx).Warning("failed to list nodes for ResourceGroup", helpers.Error(err))
		return ""
	}
	if len(nodeList.Items) == 0 {
		logger.L().Ctx(ctx).Warning("no nodes found in the cluster for ResourceGroup")
		return ""
	}

	rg := parseAzureResourceGroup(nodeList.Items[0].Spec.ProviderID)
	if rg != "" {
		logger.L().Info("successfully retrieved ResourceGroup", helpers.String("resourceGroup", rg))
	}
	return rg
}

// parseAzureResourceGroup extracts the resource group name from a Kubernetes
// node providerID using the Azure format documented at
// https://learn.microsoft.com/azure/aks/ and mirrored by the kubescape
// node-agent (see kubescape/node-agent/pkg/cloudmetadata.parseAzureResourceGroup).
func parseAzureResourceGroup(providerID string) string {
	idx := strings.Index(strings.ToLower(providerID), strings.ToLower(azureResourceGroupMarker))
	if idx == -1 {
		return ""
	}
	rest := providerID[idx+len(azureResourceGroupMarker):]
	end := strings.Index(rest, "/")
	if end == -1 {
		return rest
	}
	return rest[:end]
}

func getApiServerGitVersion(k8sApi *k8sinterface.KubernetesApi) (string, error) {
	serverVersion, err := k8sApi.KubernetesClient.Discovery().ServerVersion()
	if err != nil {
		return "Unknown", err
	}

	return serverVersion.GitVersion, nil
}
