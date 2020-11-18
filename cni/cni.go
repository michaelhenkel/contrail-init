package cni

import (
	"github.com/michaelhenkel/contrail-init/k8s"
)

type Cni struct {
	K8S *k8s.K8S
}

func (c *Cni) CreateConfig() error {
	cniConfig := `{
"cniVersion": "0.3.1",
"contrail" : {
    "vrouter-ip"    : "127.0.0.1",
    "vrouter-port"  : 9091,
    "config-dir"    : "/var/lib/contrail/ports/vm",
    "poll-timeout"  : 5,
    "poll-retries"  : 5,
    "log-file"      : "/var/log/contrail/cni/opencontrail.log",
    "log-level"     : "4",
    "cnisocket-path": "/var/run/contrail/cni.socket"
},
"name": "contrail-k8s-cni",
"type": "contrail-k8s-cni"
}`
	return c.K8S.CreateConfig(cniConfig, "10-contrail.conf")
}

func (c *Cni) CreateCertificate() error {
	return c.K8S.CreateCertificate()
}

func (c *Cni) SetOwnerNameLabel() error {
	return c.K8S.SetOwnerNameLabel()
}
