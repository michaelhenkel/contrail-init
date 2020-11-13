package k8s

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net"
	"time"

	"k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
)

type K8S struct {
	ClusterIP   string
	ClusterPort int32
	Namespace   string
	Hostname    string
	ClientSet   *kubernetes.Clientset
	Service     *corev1.Service
	Pod         *corev1.Pod
	Type        string
	OwnerName   string
	OwnerLabels map[string]string
	PodName     string
	PodIP       string
}

func (k *K8S) UpdatePOD() error {
	ctx := context.Background()
	_, err := k.ClientSet.CoreV1().Pods(k.Namespace).Update(ctx, k.Pod, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func (k *K8S) SetOwnerNameLabel() error {
	ctx := context.Background()
	pod, err := k.ClientSet.CoreV1().Pods(k.Namespace).Get(ctx, k.PodName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	for _, ownerRefernce := range pod.OwnerReferences {
		switch ownerRefernce.Kind {
		case "DaemonSet":
			daemonSet, err := k.ClientSet.AppsV1().DaemonSets(k.Namespace).Get(ctx, ownerRefernce.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			k.OwnerLabels = daemonSet.Labels
			k.OwnerName = daemonSet.Name
		case "ReplicaSet":
			replicaSet, err := k.ClientSet.AppsV1().ReplicaSets(k.Namespace).Get(ctx, ownerRefernce.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			for _, replicaSetOwnerReference := range replicaSet.OwnerReferences {
				if replicaSetOwnerReference.Kind == "Deployment" {
					deployment, err := k.ClientSet.AppsV1().Deployments(k.Namespace).Get(ctx, replicaSetOwnerReference.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					k.OwnerLabels = deployment.Labels
					k.OwnerName = deployment.Name
				}
			}
		case "StatefulSet":
			statefulSet, err := k.ClientSet.AppsV1().StatefulSets(k.Namespace).Get(ctx, ownerRefernce.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			k.OwnerLabels = statefulSet.Labels
			k.OwnerName = statefulSet.Name
		}
	}
	return nil
}

func (k *K8S) CreateConfig(configData string) error {
	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.OwnerName + "-configmap",
			Namespace: k.Namespace,
		},
		Data: map[string]string{k.Type + "-" + k.Hostname + ".conf": configData},
	}

	ctx := context.Background()
	_, err := k.ClientSet.CoreV1().ConfigMaps(k.Namespace).Get(ctx, configMap.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = k.ClientSet.CoreV1().ConfigMaps(k.Namespace).Create(ctx, configMap, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		_, err = k.ClientSet.CoreV1().ConfigMaps(k.Namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func (k *K8S) CreateCertificate() error {
	csrRequest, privateKey, err := generateCsr(k.ClusterIP, k.Hostname)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.OwnerName + "-secret",
			Namespace: k.Namespace,
		},
		Data: map[string][]byte{k.OwnerName + "-key-" + k.Hostname + ".pem": privateKey},
	}

	csr := &v1beta1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: k.OwnerName + "-csr-" + k.Hostname,
		},
		Spec: v1beta1.CertificateSigningRequestSpec{
			Groups:  []string{"system:authenticated"},
			Request: csrRequest,
			Usages: []v1beta1.KeyUsage{
				"digital signature",
				"key encipherment",
				"server auth",
				"client auth",
			},
		},
	}

	ctx := context.Background()
	_, err = k.ClientSet.CertificatesV1beta1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	for {
		created, err := k.csrCreated(csr)
		if created {
			break
		}
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(time.Second * 2))
	}

	var conditionType v1beta1.RequestConditionType
	conditionType = "Approved"
	csrCondition := v1beta1.CertificateSigningRequestCondition{
		Type:    conditionType,
		Reason:  "ContrailApprove",
		Message: "This Certificate was approved by operator approve.",
	}

	csr.Status.Conditions = []v1beta1.CertificateSigningRequestCondition{csrCondition}
	_, err = k.ClientSet.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(ctx, csr, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	var signedCert *[]byte
	var signed bool
	for {
		signed, signedCert, err = k.csrSigned(csr)
		if signed {
			break
		}
		if err != nil {
			return err
		}
		time.Sleep(time.Duration(time.Second * 2))
	}

	var pemClient []byte
	pemClient = append(pemClient, *signedCert...)
	pemClient = append(pemClient, privateKey...)
	secret.Data[k.OwnerName+"-pem-"+k.Hostname+".pem"] = pemClient

	_, err = k.ClientSet.CoreV1().Secrets(k.Namespace).Get(ctx, secret.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = k.ClientSet.CoreV1().Secrets(k.Namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		} else {
			return err
		}
	} else {
		_, err = k.ClientSet.CoreV1().Secrets(k.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	if err = k.ClientSet.CertificatesV1beta1().CertificateSigningRequests().Delete(ctx, csr.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	return nil
}

func (k *K8S) csrCreated(csr *v1beta1.CertificateSigningRequest) (bool, error) {
	ctx := context.Background()
	csr, err := k.ClientSet.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (k *K8S) csrSigned(csr *v1beta1.CertificateSigningRequest) (bool, *[]byte, error) {
	ctx := context.Background()
	csr, err := k.ClientSet.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
	if err != nil {
		return false, nil, err
	}
	if csr.Status.Certificate == nil || len(csr.Status.Certificate) == 0 {
		fmt.Println("Waiting for CSR to be signed...")
		return false, nil, nil
	}
	signedCert := csr.Status.Certificate
	return true, &signedCert, nil
}

func generateCsr(ipAddress string, hostname string) ([]byte, []byte, error) {
	certPrivKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	certPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})
	privateKeyBuffer, err := ioutil.ReadAll(certPrivKeyPEM)
	if err != nil {
		fmt.Println("cannot read certPrivKeyPEM to privateKeyBuffer")
		return nil, nil, err
	}
	csrTemplate := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:         "kubernetes-admin",
			Country:            []string{"US"},
			Province:           []string{"CA"},
			Locality:           []string{"Sunnyvale"},
			Organization:       []string{"system:masters"},
			OrganizationalUnit: []string{"Contrail"},
		},
		DNSNames:       []string{hostname},
		EmailAddresses: []string{"test@email.com"},
		IPAddresses:    []net.IP{net.ParseIP(ipAddress)},
	}
	buf := new(bytes.Buffer)
	csrBytes, _ := x509.CreateCertificateRequest(rand.Reader, &csrTemplate, certPrivKey)
	pem.Encode(buf, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
	pemBuf, err := ioutil.ReadAll(buf)
	if err != nil {
		fmt.Println("cannot read buf to pemBuf")
		return nil, nil, err
	}

	return pemBuf, privateKeyBuffer, nil
}
