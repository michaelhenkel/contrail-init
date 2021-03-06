package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/michaelhenkel/contrail-init/cni"
	"github.com/michaelhenkel/contrail-init/control"

	"github.com/michaelhenkel/contrail-init/vrouter"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	k8sv1 "github.com/michaelhenkel/contrail-init/k8s"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	caCommonName         = "contrail-signer"
	caCertValidityPeriod = 10 * 365 * 24 * time.Hour // 10 years
	certValidityPeriod   = 10 * 365 * 24 * time.Hour // 10 years
	caCertKeyLength      = 2048
	certKeyLength        = 2048
)

var err error

// ContrailInit is the Contrail Init interface
type ContrailInit interface {
	CreateConfig() error
	CreateCertificate() error
	SetOwnerNameLabel() error
}

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	namespace := os.Getenv("NAMESPACE")

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	masterLabel := metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/master=",
	}
	masterNodeList, err := clientset.CoreV1().Nodes().List(ctx, masterLabel)
	if err != nil {
		panic(err)
	}
	var masterAddresses []string
	for _, node := range masterNodeList.Items {
		for _, address := range node.Status.Addresses {
			if address.Type == v1.NodeHostName {
				masterAddresses = append(masterAddresses, address.Address)
			}
		}
	}

	/*
		kubernetesService, err := clientset.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
		if err != nil {
			panic(err)
		}
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, os.Getenv("PODNAME"), metav1.GetOptions{})
		if err != nil {
			panic(err)
		}

		k8s := &k8s.K8S{
			ClusterIP:   kubernetesService.Spec.ClusterIP,
			ClusterPort: kubernetesService.Spec.Ports[0].Port,
			Namespace:   namespace,
			Hostname:    os.Getenv("HOSTNAME"),
			ClientSet:   clientset,
			Service:     kubernetesService,
			PodName:     os.Getenv("PODNAME"),
			Type:        os.Getenv("APP"),
			PodIP:       os.Getenv("PODIP"),
			Pod:         pod,
		}
	*/
	k8s, err := k8sv1.New(clientset, namespace)
	if err != nil {
		panic(err)
	}

	var contrailInit ContrailInit

	switch k8s.OwnerLabels["app"] {
	case "contrail-control":
		controlInit := &control.Control{
			K8S: k8s,
		}
		contrailInit = controlInit
	case "contrail-vrouter":
		vrouterInit := &vrouter.Vrouter{
			K8S: k8s,
		}
		contrailInit = vrouterInit
	case "contrail-cni":
		cniInit := &cni.Cni{
			K8S: k8s,
		}
		contrailInit = cniInit
	default:
		fmt.Println("missing service, control/vrouter are supported")
		os.Exit(1)
	}

	if err := contrailInit.CreateConfig(); err != nil {
		panic(err)
	}

	if err := contrailInit.CreateCertificate(); err != nil {
		panic(err)
	}
}
