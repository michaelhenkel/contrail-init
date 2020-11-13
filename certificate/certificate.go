package certificate

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

	"k8s.io/api/certificates/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

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

func csrCreated(clientset *kubernetes.Clientset, csr *v1beta1.CertificateSigningRequest) (bool, error) {
	ctx := context.Background()
	csr, err = clientset.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func csrSigned(clientset *kubernetes.Clientset, csr *v1beta1.CertificateSigningRequest) (bool, *[]byte, error) {
	ctx := context.Background()
	csr, err = clientset.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
	if err != nil {
		return false, nil, err
	}
	if csr.Status.Certificate == nil || len(csr.Status.Certificate) == 0 {
		fmt.Println("Waiting for CSR to be signed...")
		return false, nil, nil
	}
	fmt.Println("csr signed:", string(csr.Status.Certificate))
	signedCert := csr.Status.Certificate
	return true, &signedCert, nil
}
