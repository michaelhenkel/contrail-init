package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	corev1 "k8s.io/api/core/v1"
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

func main() {
	inClusterPtr := flag.Bool("inCluster", true, "inCluster falg")
	namespacePtr := flag.String("namespace", "contrail", "Contrail Namespace")
	hostnamePtr := flag.String("hostname", "", "hostname")
	certpathPtr := flag.String("cert", "", "path to crt")
	serverIPPtr := flag.String("serverip", "", "serverIP")

	flag.Parse()

	hostname := *hostnamePtr
	namespace := *namespacePtr
	certpath := *certpathPtr
	serverIP := *serverIPPtr

	config := &rest.Config{}
	if *inClusterPtr {
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
		hostname = os.Getenv("HOSTNAME")
		namespace = os.Getenv("NAMESPACE")
		serverIP = os.Getenv("PODIP")
		certpath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	} else {
		var kubeconfig *string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		if hostname == "" {
			fmt.Println("for out of cluster -hostname must be specified")
			os.Exit(1)
		}

		if certpath == "" {
			fmt.Println("for out of cluster -cert must be specified")
			os.Exit(1)
		}

		if serverIP == "" {
			fmt.Println("for out of cluster -serverip must be specified")
			os.Exit(1)
		}

		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err)
		}

	}

	fmt.Println("ca data:", string(config.CAData))
	fmt.Println("cert data:", string(config.CertData))
	fmt.Println("key data:", string(config.KeyData))
	var clientPEM []byte
	clientPEM = append(clientPEM, config.KeyData...)
	clientPEM = append(clientPEM, config.CertData...)
	fmt.Println(string(clientPEM))

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

	kubernetesService, err := clientset.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		panic(err)
	}

	configMap := createConfigMap(kubernetesService.Spec.ClusterIP, kubernetesService.Spec.Ports[0].Port, namespace, hostname)

	_, err = clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMap.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = clientset.CoreV1().ConfigMaps(namespace).Create(ctx, configMap, metav1.CreateOptions{})
			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	} else {
		_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, configMap, metav1.UpdateOptions{})
		if err != nil {
			panic(err)
		}
	}

	/*
		outPem, err := createCSR(hostname, namespace, serverIP, certpath)
		if err != nil {
			panic(err)
		}
	*/

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "contrail-control-secret",
			Namespace: namespace,
		},
		Data: map[string][]byte{"contrail-control-" + hostname + ".pem": clientPEM},
		//Type: corev1.SecretTypeTLS,
	}

	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, secret.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	} else {
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			panic(err)
		}
	}

	/*
		csr, err := createCSR(hostname, namespace, serverIP, certpath)
		if err != nil {
			panic(err)
		}

		_, err = clientset.CertificatesV1beta1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
	*/

}

func createConfigMap(clusterIP string, port int32, namespace string, hostname string) *corev1.ConfigMap {
	controlConfig := `
[DEFAULT]
log_level=SYS_DEBUG
hostname=` + hostname + `
[CONFIGDB]
config_db_use_k8s=1
config_db_use_ssl=1
config_db_server_list=` + clusterIP + `:` + strconv.Itoa(int(port)) + `
config_db_ca_certs=/etc/contrailkeys/contrail-control-` + hostname + `.pem 
[SANDESH]
`
	configMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "contrail-control-configmap",
			Namespace: namespace,
		},
		Data: map[string]string{"contrail-control.conf": controlConfig},
	}
	return configMap
}

func createCSR(serverName string, namespace string, serverIP string, certpath string) (*[]byte, error) {

	r, err := ioutil.ReadFile(certpath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(r)

	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, err
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, err
	}

	caPEM := new(bytes.Buffer)
	pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	caPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(caPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivKey),
	})

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1658),
		Subject: pkix.Name{
			Organization:  []string{"Company, INC."},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{"Golden Gate Bridge"},
			PostalCode:    []string{"94016"},
		},
		DNSNames:     []string{serverName},
		IPAddresses:  []net.IP{net.ParseIP(serverIP)},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, err
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, err
	}

	certPEM := new(bytes.Buffer)
	pem.Encode(certPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	certPrivKeyPEM := new(bytes.Buffer)
	pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})
	outPem := certPEM.Bytes()
	outPem = append(outPem, certPrivKeyPEM.Bytes()...)
	outPem = append(outPem, caPEM.Bytes()...)

	return &outPem, nil

	/*
		singerName := "kubernetes.io/kube-apiserver-client"
		csr := &certv1.CertificateSigningRequest{
			TypeMeta: metav1.TypeMeta{
				Kind:       "CertificateSigningRequest",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "control-csr-" + serverName,
			},
			Spec: certv1.CertificateSigningRequestSpec{
				Request:    certBytes,
				SignerName: &singerName,
				Usages:     []certv1.KeyUsage{"client auth", "server auth"},
			},
		}
		return csr, nil
	*/
}

type CertificateSubject struct {
	name           string
	hostname       string
	ip             string
	alternativeIPs []string
}

func NewSubject(name string, hostname string, ip string, alternativeIPs []string) CertificateSubject {
	return CertificateSubject{name: name, hostname: hostname, ip: ip, alternativeIPs: alternativeIPs}
}

func (c CertificateSubject) generateCertificateTemplate() (x509.Certificate, *rsa.PrivateKey, error) {
	certPrivKey, err := rsa.GenerateKey(rand.Reader, certKeyLength)

	if err != nil {
		return x509.Certificate{}, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(certValidityPeriod)

	serialNumber, err := generateSerialNumber()
	if err != nil {
		return x509.Certificate{}, nil, fmt.Errorf("fail to generate serial number: %w", err)
	}

	var ips []net.IP
	ips = append(ips, net.ParseIP(c.ip))
	for _, ip := range c.alternativeIPs {
		ips = append(ips, net.ParseIP(ip))
	}

	certificateTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:         c.ip,
			Country:            []string{"US"},
			Province:           []string{"CA"},
			Locality:           []string{"Sunnyvale"},
			Organization:       []string{"Juniper Networks"},
			OrganizationalUnit: []string{"Contrail"},
		},
		DNSNames:    []string{c.hostname},
		IPAddresses: ips,
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	return certificateTemplate, certPrivKey, nil
}

func generateCaCertificateTemplate() (x509.Certificate, *rsa.PrivateKey, error) {
	caPrivKey, err := rsa.GenerateKey(rand.Reader, caCertKeyLength)

	if err != nil {
		return x509.Certificate{}, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	notBefore := time.Now()
	notAfter := notBefore.Add(caCertValidityPeriod)

	serialNumber, err := generateSerialNumber()
	if err != nil {
		return x509.Certificate{}, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	caCertTemplate := x509.Certificate{
		SerialNumber:          serialNumber,
		BasicConstraintsValid: true,
		IsCA:                  true,
		Subject: pkix.Name{
			CommonName:         caCommonName,
			Country:            []string{"US"},
			Province:           []string{"CA"},
			Locality:           []string{"Sunnyvale"},
			Organization:       []string{"Juniper Networks"},
			OrganizationalUnit: []string{"Contrail"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	return caCertTemplate, caPrivKey, nil

}

func generateSerialNumber() (*big.Int, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialNumberLimit)
}
