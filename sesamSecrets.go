package main

import "github.com/3lvia/hn-config-lib-go/vault"

const pathSecrets = "sesam-extensions/kv/data/manual/prometheus-exporter"

type SesamSecrets struct {
  sesamData map[string]string
}

func (d *SesamSecrets) GetSubscriptionSpec() vault.SecretSubscriptionSpec{
  return vault.SecretSubscriptionSpec{
    Paths: []string{pathSecrets},
  }
}

func (d *SesamSecrets) ReceiveAtStartup(secret vault.UpdatedSecret){
  if secret.Path == pathSecrets {
    d.sesamData = secret.GetAllData()
    return
  }
}

func (d *SesamSecrets) StartSecretsListener(){}

func (d *SesamSecrets) Host() string {
  return d.sesamData["sesam_host"]
}

func (d *SesamSecrets) Desc() string {
  return d.sesamData["host_desc"]
}

func (d *SesamSecrets) Jwt() string {
  return d.sesamData["host_jwt"]
}
