package vrouter

import (
	"context"
	"net"
	"strconv"

	"github.com/michaelhenkel/contrail-init/k8s"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Vrouter struct {
	K8S *k8s.K8S
}

func (v *Vrouter) CreateConfig() error {
	controlNodeName, controlNodePort, err := v.GetControlNode()
	if err != nil {
		return err
	}
	vrouterConfig := `[DEFAULT]
xmppport=` + strconv.Itoa((int(controlNodePort))) + `
controlnode=` + controlNodeName + ``

	v.K8S.Pod.Labels["controlNodeName"] = controlNodeName
	if err := v.setInterfaceLabel(); err != nil {
		return err
	}
	if err := v.K8S.UpdatePOD(); err != nil {
		return err
	}
	return v.K8S.CreateConfig(vrouterConfig)
}

func (v *Vrouter) setInterfaceLabel() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, iface := range ifaces {
		ifaceAddresses, err := iface.Addrs()
		if err != nil {
			return err
		}
		for _, ifaceAddress := range ifaceAddresses {
			switch addressValue := ifaceAddress.(type) {
			case *net.IPAddr:
				if addressValue.IP.String() == v.K8S.PodIP {
					v.K8S.Pod.Labels["interface"] = iface.Name
					return nil
				}
			}
		}
	}
	return nil
}

func (v *Vrouter) CreateCertificate() error {
	return v.K8S.CreateCertificate()
}

func (v *Vrouter) SetOwnerNameLabel() error {
	return v.K8S.SetOwnerNameLabel()
}

func (v *Vrouter) GetControlNode() (string, int32, error) {

	controlNodeName, ok := v.K8S.OwnerLabels["contrail-control-instance"]
	if !ok {
		controlNodeName = "contrail-control"
	}
	ctx := context.Background()
	controlNodeService, err := v.K8S.ClientSet.CoreV1().Services(v.K8S.Namespace).Get(ctx, controlNodeName, metav1.GetOptions{})
	if err != nil {
		return "", 0, err
	}
	var controlNodePort int32
	controlNodePort = 5269
	for _, port := range controlNodeService.Spec.Ports {
		if port.Name == "xmpp" {
			controlNodePort = port.Port
		}
	}

	return controlNodeService.Spec.ClusterIP, controlNodePort, nil
}
