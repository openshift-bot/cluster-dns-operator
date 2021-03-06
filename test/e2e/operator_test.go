// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	operatorclient "github.com/openshift/cluster-dns-operator/pkg/operator/client"
	operatorcontroller "github.com/openshift/cluster-dns-operator/pkg/operator/controller"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// upstreamPodName is the name of the upstream CoreDNS server
	// used for testing DNS forwarding.
	upstreamPodName = "test-upstream"
	// upstreamPodNs is the namespace of the upstream CoreDNS server
	// used for testing DNS forwarding.
	upstreamPodNs = "openshift-dns"
	// upstreamCorefile is the Corefile used by the upstream CoreDNS server
	// used for testing DNS forwarding.
	upstreamCorefile = `.:5353 {
    hosts {
      1.2.3.4 www.foo.com
    }
    health
    errors
    log
}
`
)

func getClient() (client.Client, error) {
	// Get a kube client.
	kubeConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kube config: %v", err)
	}
	kubeClient, err := operatorclient.NewClient(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %v", err)
	}
	return kubeClient, nil
}

func TestOperatorAvailable(t *testing.T) {
	cl, err := getClient()
	if err != nil {
		t.Fatal(err)
	}

	err = wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		co := &configv1.ClusterOperator{}
		if err := cl.Get(context.TODO(), types.NamespacedName{Name: operatorcontroller.DNSOperatorName}, co); err != nil {
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == configv1.OperatorAvailable &&
				cond.Status == configv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("did not get expected available condition: %v", err)
	}
}

func TestDefaultDNSExists(t *testing.T) {
	cl, err := getClient()
	if err != nil {
		t.Fatal(err)
	}

	err = wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		dns := &operatorv1.DNS{}
		if err := cl.Get(context.TODO(), types.NamespacedName{Name: "default"}, dns); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("failed to get default dns: %v", err)
	}
}

func TestVersionReporting(t *testing.T) {
	cl, err := getClient()
	if err != nil {
		t.Fatal(err)
	}

	deployment := &appsv1.Deployment{}
	namespacedName := types.NamespacedName{Namespace: "openshift-dns-operator", Name: "dns-operator"}
	err = wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		if err := cl.Get(context.TODO(), namespacedName, deployment); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("failed to get deployment: %v", err)
	}

	var curVersion string
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "RELEASE_VERSION" {
			curVersion = env.Value
			break
		}
	}
	if len(curVersion) == 0 {
		t.Errorf("env RELEASE_VERSION not found in the operator deployment")
	}

	newVersion := "0.0.1-test"
	setVersion(deployment, newVersion)
	if err := cl.Update(context.TODO(), deployment); err != nil {
		t.Fatalf("failed to update dns operator to new version: %v", err)
	}
	defer func() {
		if err := cl.Get(context.TODO(), namespacedName, deployment); err != nil {
			t.Fatalf("failed to get latest deployment: %v", err)
		}
		setVersion(deployment, curVersion)
		if err := cl.Update(context.TODO(), deployment); err != nil {
			t.Fatalf("failed to restore dns operator to old release version: %v", err)
		}
	}()

	err = wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		co := &configv1.ClusterOperator{}
		if err := cl.Get(context.TODO(), types.NamespacedName{Name: operatorcontroller.DNSOperatorName}, co); err != nil {
			return false, nil
		}

		for _, v := range co.Status.Versions {
			if v.Name == "operator" {
				if v.Version == newVersion {
					return true, nil
				}
				break
			}
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("failed to observe updated version reported in dns clusteroperator status: %v", err)
	}
}

func TestCoreDNSImageUpgrade(t *testing.T) {
	cl, err := getClient()
	if err != nil {
		t.Fatal(err)
	}

	deployment := &appsv1.Deployment{}
	namespacedName := types.NamespacedName{Namespace: "openshift-dns-operator", Name: "dns-operator"}
	err = wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		if err := cl.Get(context.TODO(), namespacedName, deployment); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("failed to get deployment: %v", err)
	}

	var curImage string
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "IMAGE" {
			curImage = env.Value
			break
		}
	}
	if len(curImage) == 0 {
		t.Errorf("env IMAGE not found in the operator deployment")
	}

	newImage := "openshift/origin-coredns:latest"
	setImage(deployment, newImage)
	if err := cl.Update(context.TODO(), deployment); err != nil {
		t.Fatalf("failed to update dns operator to new coredns image: %v", err)
	}
	defer func() {
		if err := cl.Get(context.TODO(), namespacedName, deployment); err != nil {
			t.Fatalf("failed to get latest deployment: %v", err)
		}
		setImage(deployment, curImage)
		if err := cl.Update(context.TODO(), deployment); err != nil {
			t.Fatalf("failed to restore dns operator to old coredns image: %v", err)
		}
	}()

	err = wait.PollImmediate(1*time.Second, 3*time.Minute, func() (bool, error) {
		podList := &corev1.PodList{}
		if err := cl.List(context.TODO(), podList, client.InNamespace("openshift-dns")); err != nil {
			return false, nil
		}

		for _, pod := range podList.Items {
			for _, container := range pod.Spec.Containers {
				if container.Name == "dns" {
					if container.Image == newImage {
						return true, nil
					}
					break
				}
			}
		}
		return false, nil
	})
	if err != nil {
		t.Errorf("failed to observe updated coredns image: %v", err)
	}
}

func setVersion(deployment *appsv1.Deployment, version string) {
	for i, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "RELEASE_VERSION" {
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = version
			break
		}
	}
}

func setImage(deployment *appsv1.Deployment, image string) {
	for i, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "IMAGE" {
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = image
			break
		}
	}
}

func TestDNSForwarding(t *testing.T) {
	cl, err := getClient()
	if err != nil {
		t.Fatal(err)
	}

	// Create the upstream resolver ConfigMap.
	upstreamCfgMap := buildConfigMap(upstreamPodName, upstreamPodNs, "Corefile", upstreamCorefile)
	if err := cl.Create(context.TODO(), upstreamCfgMap); err != nil {
		t.Fatalf("failed to create configmap %s/%s: %v", upstreamCfgMap.Namespace, upstreamCfgMap.Name, err)
	}
	defer func() {
		if err := cl.Delete(context.TODO(), upstreamCfgMap); err != nil {
			t.Fatalf("failed to delete configmap %s/%s: %v", upstreamCfgMap.Namespace, upstreamCfgMap.Name, err)
		}
	}()

	// Get the CoreDNS image used by the test upstream resolver.
	co := &configv1.ClusterOperator{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Name: operatorcontroller.DNSOperatorName}, co); err != nil {
		t.Fatalf("failed to get clusteroperator %s: %v", operatorcontroller.DNSOperatorName, err)
	}
	var (
		coreImage      string
		coreImageFound bool
	)
	for _, ver := range co.Status.Versions {
		if ver.Name == operatorcontroller.CoreDNSVersionName {
			if len(ver.Version) == 0 {
				t.Fatalf("clusteroperator %s has empty coredns version", operatorcontroller.DNSOperatorName)
			}
			coreImageFound = true
			coreImage = ver.Version
			break
		}
	}
	if !coreImageFound {
		t.Fatalf("version %s not found for clusteroperator %s", operatorcontroller.CoreDNSVersionName, operatorcontroller.DNSOperatorName)
	}

	// Create the upstream resolver Pod.
	upstreamResolver := upstreamPod(upstreamPodName, upstreamPodNs, coreImage, upstreamPodName)
	if err := cl.Create(context.TODO(), upstreamResolver); err != nil {
		t.Fatalf("failed to create pod %s/%s: %v", upstreamResolver.Namespace, upstreamResolver.Name, err)
	}
	defer func() {
		if err := cl.Delete(context.TODO(), upstreamResolver); err != nil {
			t.Fatalf("failed to delete pod %s/%s: %v", upstreamResolver.Namespace, upstreamResolver.Name, err)
		}
	}()

	// Wait for the upstream resolver Pod to be ready.
	err = wait.PollImmediate(1*time.Second, 2*time.Minute, func() (bool, error) {
		if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: upstreamResolver.Namespace, Name: upstreamResolver.Name}, upstreamResolver); err != nil {
			return false, nil
		}
		for _, cond := range upstreamResolver.Status.Conditions {
			if cond.Type == corev1.ContainersReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("failed to observe ContainersReady condition for pod %s/%s: %v", upstreamResolver.Namespace, upstreamResolver.Name, err)
	}

	// Create the upstream resolver Service and get the ClusterIP.
	upstreamSvc := upstreamService(upstreamPodName, upstreamPodNs)
	if err := cl.Create(context.TODO(), upstreamSvc); err != nil {
		t.Fatalf("failed to create service %s/%s: %v", upstreamSvc.Namespace, upstreamSvc.Name, err)
	}
	defer func() {
		if err := cl.Delete(context.TODO(), upstreamSvc); err != nil {
			t.Fatalf("failed to delete service %s/%s: %v", upstreamSvc.Namespace, upstreamSvc.Name, err)
		}
	}()
	if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: upstreamSvc.Namespace, Name: upstreamSvc.Name}, upstreamSvc); err != nil {
		t.Fatalf("failed to get service %s/%s: %v", upstreamSvc.Namespace, upstreamSvc.Name, err)
	}
	upstreamIP := upstreamSvc.Spec.ClusterIP
	if len(upstreamIP) == 0 {
		t.Fatalf("failed to get clusterIP for service %s/%s", upstreamSvc.Namespace, upstreamSvc.Name)
	}

	// Update cluster DNS forwarding with the upstream resolver's Service IP address.
	defaultDNS := &operatorv1.DNS{}
	if err := cl.Get(context.TODO(), types.NamespacedName{Name: operatorcontroller.DefaultDNSController}, defaultDNS); err != nil {
		t.Fatalf("failed to get default dns: %v", err)
	}
	upstream := operatorv1.Server{
		Name:  "test",
		Zones: []string{"foo.com"},
		ForwardPlugin: operatorv1.ForwardPlugin{
			Upstreams: []string{upstreamIP},
		},
	}
	defaultDNS.Spec.Servers = []operatorv1.Server{upstream}
	if err := cl.Update(context.TODO(), defaultDNS); err != nil {
		t.Fatalf("failed to update dns %s: %v", defaultDNS.Name, err)
	}
	defer func() {
		defaultDNS = &operatorv1.DNS{}
		if err := cl.Get(context.TODO(), types.NamespacedName{Name: "default"}, defaultDNS); err != nil {
			t.Fatalf("failed to get default dns: %v", err)
		}
		if len(defaultDNS.Spec.Servers) != 0 {
			// dnses.operator/default has a nil spec by default.
			defaultDNS.Spec = operatorv1.DNSSpec{}
			if err := cl.Update(context.TODO(), defaultDNS); err != nil {
				t.Fatalf("failed to update dns %s: %v", defaultDNS.Name, err)
			}
		}
	}()

	// Verify that the Corefile of DNS DaemonSet pods have been updated.
	dnsDaemonSet := &appsv1.DaemonSet{}
	if err := cl.Get(context.TODO(), operatorcontroller.DNSDaemonSetName(defaultDNS), dnsDaemonSet); err != nil {
		fmt.Errorf("failed to get daemonset %s/%s: %v", dnsDaemonSet.Namespace, dnsDaemonSet.Name, err)
	}
	selector, err := metav1.LabelSelectorAsSelector(dnsDaemonSet.Spec.Selector)
	if err != nil {
		t.Fatalf("daemonset %s/%s has invalid spec.selector: %v", dnsDaemonSet.Namespace, dnsDaemonSet.Name, err)
	}
	defaultDNSPods := &corev1.PodList{}
	if err := cl.List(context.TODO(), defaultDNSPods, client.MatchingLabelsSelector{Selector: selector}, client.InNamespace(dnsDaemonSet.Namespace)); err != nil {
		t.Fatalf("failed to list pods for dns daemonset %s/%s: %v", dnsDaemonSet.Namespace, dnsDaemonSet.Name, err)
	}
	catCmd := []string{"cat", "/etc/coredns/Corefile"}
	for _, pod := range defaultDNSPods.Items {
		if err := lookForStringInPodExec(pod.Namespace, pod.Name, "dns", catCmd, upstreamIP, 2*time.Minute); err != nil {
			t.Fatalf("failed to find %s in %s of pod %s/%s: %v", upstreamIP, catCmd[1], pod.Namespace, pod.Name, err)
		}
	}

	// Get the openshift-cli image.
	var (
		cliImage      string
		cliImageFound bool
	)
	for _, ver := range co.Status.Versions {
		if ver.Name == operatorcontroller.OpenshiftCLIVersionName {
			if len(ver.Version) == 0 {
				break
			}
			cliImage = ver.Version
			cliImageFound = true
			break
		}
	}
	if !cliImageFound {
		t.Fatalf("failed to find the %s version for clusteroperator %s", operatorcontroller.OpenshiftCLIVersionName, co.Name)
	}

	// Create the client Pod.
	testClient := buildPod("test-client", "default", cliImage, []string{"sleep", "3600"})
	if err := cl.Create(context.TODO(), testClient); err != nil {
		t.Fatalf("failed to create pod %s/%s: %v", testClient.Namespace, testClient.Name, err)
	}
	defer func() {
		if err := cl.Delete(context.TODO(), testClient); err != nil {
			t.Fatalf("failed to delete pod %s/%s: %v", testClient.Namespace, testClient.Name, err)
		}
	}()
	// Wait for the client Pod to be ready.
	err = wait.PollImmediate(1*time.Second, 60*time.Second, func() (bool, error) {
		if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: testClient.Namespace, Name: testClient.Name}, testClient); err != nil {
			return false, nil
		}
		for _, cond := range testClient.Status.Conditions {
			if cond.Type == corev1.ContainersReady &&
				cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("failed to observe ContainersReady condition for pod %s/%s: %v", testClient.Namespace, testClient.Name, err)
	}
	// Dig the example dns forwarding host.
	digCmd := []string{"dig", "+short", "www.foo.com", "A"}
	fooHost := "1.2.3.4"
	if err := lookForStringInPodExec(testClient.Namespace, testClient.Name, testClient.Name, digCmd, fooHost, 30*time.Second); err != nil {
		t.Fatalf("failed to dig %s: %v", upstreamIP, err)
	}
	// Scrape the upstream resolver logs for the "NOERROR" message.
	logMsg := "NOERROR"
	if err := lookForStringInPodLog(upstreamResolver.Namespace, upstreamResolver.Name, upstreamResolver.Name, logMsg, 30*time.Second); err != nil {
		t.Fatalf("failed to parse %q from pod %s/%s logs: %v", logMsg, upstreamResolver.Namespace, upstreamResolver.Name, err)
	}
}
