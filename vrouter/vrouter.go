package vrouter

import (
	"github.com/michaelhenkel/contrail-init/k8s"
)

type Vrouter struct {
	K8S *k8s.K8S
}

func (v *Vrouter) CreateConfig() error {
	vrouterConfig := `
	`
	return v.K8S.CreateConfig(vrouterConfig)
}

func (v *Vrouter) CreateCertificate() error {
	return v.K8S.CreateCertificate()
}
