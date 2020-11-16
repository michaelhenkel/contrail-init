package vrouter

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

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
	v.K8S.Pod.Labels["controlNodeName"] = controlNodeName
	if err := v.setInterfaceLabel(); err != nil {
		return err
	}
	if err := v.K8S.UpdatePOD(); err != nil {
		return err
	}
	if err := v.GetCIDR(); err != nil {
		return err
	}
	if err := v.GetGateway(); err != nil {
		return err
	}
	vrouterConfig := `[CONTROL-NODE]
	server=` + controlNodeName + `:` + strconv.Itoa((int(controlNodePort))) + `
	
	[DEFAULT]	
	debug=1
	hostname=` + v.K8S.Hostname + `
	# http_server_port=8085
	# log_category=
	# log_file=/var/log/contrail/vrouter.log
	# log_level=SYS_DEBUG
	# log_local=0
	# log_flow=0
	# tunnel_type=
	# headless_mode=
	
	[DISCOVERY]
	
	[DNS]
	# server=10.0.0.1 10.0.0.2
	
	[HYPERVISOR]
	# type=kvm
	
	[FLOWS]
	# max_vm_flows=
	# max_system_linklocal_flows=4096
	# max_vm_linklocal_flows=1024
	
	[METADATA]
	# metadata_proxy_secret=contrail
	
	[NETWORKS]
	control_network_ip=
	
	[VIRTUAL-HOST-INTERFACE]
	name=vhost0
	ip=` + v.K8S.PodIP + `/` + v.K8S.Pod.Labels["mask"] + `
	gateway=10.1.1.254
	physical_interface=` + v.K8S.Pod.Labels["interface"] + `
	
	[GATEWAY-0]
	
	[GATEWAY-1]

	
	[SERVICE-INSTANCE]
	netns_command=/usr/bin/opencontrail-vrouter-netns
	#netns_workers=1
	#netns_timeout=30`

	return v.K8S.CreateConfig(vrouterConfig)
}

func (v *Vrouter) setInterfaceLabel() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	vhostExists := false
	var vhostMac net.HardwareAddr
	for _, iface := range ifaces {
		if iface.Name == "vhost0" {
			vhostExists = true
			vhostMac = iface.HardwareAddr
		}
	}
	if vhostExists {
		v.K8S.Pod.Labels["vhostExists"] = "true"
		for _, iface := range ifaces {
			if iface.HardwareAddr.String() == vhostMac.String() {
				v.K8S.Pod.Labels["interface"] = iface.Name
				v.K8S.Pod.Labels["mac"] = iface.HardwareAddr.String()
			}
		}
	} else {
		v.K8S.Pod.Labels["vhostExists"] = "false"
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
						v.K8S.Pod.Labels["mac"] = iface.HardwareAddr.String()
					}
				}
			}
		}
	}
	return nil
}

func (v *Vrouter) GetCIDR() error {
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
					mask := addressValue.IP.DefaultMask()
					var cidrMask uint32
					for idx, dotpart := range strings.Split(mask.String(), ".") {
						part, _ := strconv.Atoi(dotpart)
						cidrMask = cidrMask | uint32(part)<<uint32(24-idx*8)
					}
					v.K8S.Pod.Labels["mask"] = strconv.Itoa(int(cidrMask))
				}
			}
		}
	}
	return nil
}

func (v *Vrouter) GetGateway() error {
	var gateway string
	if gw, ok := v.K8S.OwnerLabels["Gateway"]; ok {
		gateway = gw
	} else {
		r := strings.NewReader("/proc/net/route")
		routes, err := GetRoutes(r)
		if err != nil {
			fmt.Printf("ERROR: %v", err)
			return err
		}
		for i := range routes {
			zero := net.IP{0, 0, 0, 0}
			if routes[i].Destination.Equal(zero) {
				gateway = routes[i].Gateway.String()
			}
		}
	}
	v.K8S.Pod.Labels["gateway"] = gateway
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

type Route struct {
	Interface   string
	Destination net.IP
	Gateway     net.IP
	// TODO: add more fields here if needed
}

func GetRoutes(file io.Reader) ([]Route, error) {
	routes := []Route{}

	scanner := bufio.NewReader(file)
	lineNum := 0
	for {
		line, err := scanner.ReadString('\n')
		if err == io.EOF {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("wrong number of fields (expected at least 3, got %d): %s", len(fields), line)
		}
		lineNum++
		if lineNum == 1 {
			continue // skip header
		}
		routes = append(routes, Route{})
		route := &routes[len(routes)-1]
		route.Interface = fields[0]
		ip, err := parseIP(fields[1])
		if err != nil {
			return nil, err
		}
		route.Destination = ip
		ip, err = parseIP(fields[2])
		if err != nil {
			return nil, err
		}
		route.Gateway = ip
	}
	return routes, nil
}

func parseIP(str string) (net.IP, error) {
	bytes, err := hex.DecodeString(str)
	if err != nil {
		return nil, err
	}
	if len(bytes) != net.IPv4len {
		// TODO: IPv6 support
		return nil, fmt.Errorf("only IPv4 is supported")
	}
	bytes[0], bytes[1], bytes[2], bytes[3] = bytes[3], bytes[2], bytes[1], bytes[0]
	return net.IP(bytes), nil
}
