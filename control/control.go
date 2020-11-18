package control

import (
	"strconv"

	"github.com/michaelhenkel/contrail-init/k8s"
)

type Control struct {
	K8S *k8s.K8S
}

func (c *Control) CreateConfig() error {
	controlConfig := `[DEFAULT]
log_level=SYS_DEBUG
hostname=` + c.K8S.Hostname + `
[CONFIGDB]
config_db_use_k8s=1
config_db_use_ssl=1
config_db_server_list=` + c.K8S.ClusterIP + `:` + strconv.Itoa(int(c.K8S.ClusterPort)) + `
config_db_ca_certs=/etc/contrailkeys/contrail-control-pem-` + c.K8S.Hostname + `.pem
[SANDESH]`
	return c.K8S.CreateConfig(controlConfig, c.K8S.Type+"-"+c.K8S.Hostname+".conf")
}

func (c *Control) CreateCertificate() error {
	return c.K8S.CreateCertificate()
}

func (c *Control) SetOwnerNameLabel() error {
	return c.K8S.SetOwnerNameLabel()
}
