package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"

	"ddns-agent/internal/provider/constants"
)

type Provider interface {
	String() string
	Domain() string
	Owner() string
	BuildDomainName() string
	IPVersion() constants.IPVersion
	Proxied() bool
	Update(ctx context.Context, client *http.Client, ip netip.Addr) (netip.Addr, error)
}

type ProviderField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

type ProviderInfo struct {
	Name   constants.Provider `json:"name"`
	Label  string             `json:"label"`
	Fields []ProviderField    `json:"fields"`
}

func New(name constants.Provider, data json.RawMessage, domain, owner string,
	ipVersion constants.IPVersion) (Provider, error) {

	switch name {
	case constants.Cloudflare:
		return newCloudflare(data, domain, owner, ipVersion)
	case constants.DuckDNS:
		return newDuckDNS(data, domain, owner, ipVersion)
	case constants.Namecheap:
		return newNamecheap(data, domain, owner)
	case constants.GoDaddy:
		return newGoDaddy(data, domain, owner, ipVersion)
	case constants.DigitalOcean:
		return newDigitalOcean(data, domain, owner, ipVersion)
	case constants.NoIP:
		return newNoIP(data, domain, owner, ipVersion)
	case constants.Hetzner:
		return newHetzner(data, domain, owner, ipVersion)
	case constants.Porkbun:
		return newPorkbun(data, domain, owner, ipVersion)
	case constants.DeSEC:
		return newDeSEC(data, domain, owner, ipVersion)
	case constants.OVH:
		return newOVH(data, domain, owner, ipVersion)
	case constants.Strato:
		return newStrato(data, domain, owner)
	case constants.INWX:
		return newINWX(data, domain, owner, ipVersion)
	case constants.Ionos:
		return newIonos(data, domain, owner, ipVersion)
	case constants.Route53:
		return newRoute53(data, domain, owner, ipVersion)
	case constants.Vultr:
		return newVultr(data, domain, owner, ipVersion)
	case constants.Linode:
		return newLinode(data, domain, owner, ipVersion)
	case constants.Gandi:
		return newGandi(data, domain, owner, ipVersion)
	case constants.Dynu:
		return newDynu(data, domain, owner, ipVersion)
	case constants.DynV6:
		return newDynV6(data, domain, owner, ipVersion)
	case constants.FreeDNS:
		return newFreeDNS(data, domain, owner, ipVersion)
	case constants.EasyDNS:
		return newEasyDNS(data, domain, owner, ipVersion)
	case constants.DNSOMatic:
		return newDNSOMatic(data, domain, owner, ipVersion)
	case constants.DNSPod:
		return newDNSPod(data, domain, owner, ipVersion)
	case constants.LuaDNS:
		return newLuaDNS(data, domain, owner, ipVersion)
	case constants.HE:
		return newHE(data, domain, owner, ipVersion)
	case constants.Njalla:
		return newNjalla(data, domain, owner, ipVersion)
	case constants.Infomaniak:
		return newInfomaniak(data, domain, owner, ipVersion)
	case constants.DDNSS:
		return newDDNSS(data, domain, owner, ipVersion)
	case constants.NameSilo:
		return newNameSilo(data, domain, owner)
	case constants.NameCom:
		return newNameCom(data, domain, owner, ipVersion)
	case constants.Dreamhost:
		return newDreamhost(data, domain, owner, ipVersion)
	case constants.Domeneshop:
		return newDomeneshop(data, domain, owner, ipVersion)
	case constants.DonDominio:
		return newDonDominio(data, domain, owner, ipVersion)
	case constants.Aliyun:
		return newAliyun(data, domain, owner, ipVersion)
	case constants.AllInkl:
		return newAllInkl(data, domain, owner, ipVersion)
	case constants.ChangeIP:
		return newChangeIP(data, domain, owner, ipVersion)
	case constants.DD24:
		return newDD24(data, domain, owner, ipVersion)
	case constants.Dyn:
		return newDyn(data, domain, owner, ipVersion)
	case constants.GoIP:
		return newGoIP(data, domain, owner, ipVersion)
	case constants.GCP:
		return newGCP(data, domain, owner, ipVersion)
	case constants.Loopia:
		return newLoopia(data, domain, owner, ipVersion)
	case constants.MyAddr:
		return newMyAddr(data, domain, owner, ipVersion)
	case constants.Netcup:
		return newNetcup(data, domain, owner, ipVersion)
	case constants.NowDNS:
		return newNowDNS(data, domain, owner, ipVersion)
	case constants.OpenDNS:
		return newOpenDNS(data, domain, owner, ipVersion)
	case constants.SelfhostDe:
		return newSelfhostDe(data, domain, owner, ipVersion)
	case constants.Servercow:
		return newServercow(data, domain, owner, ipVersion)
	case constants.Spdyn:
		return newSpdyn(data, domain, owner, ipVersion)
	case constants.Variomedia:
		return newVariomedia(data, domain, owner, ipVersion)
	case constants.ZoneEdit:
		return newZoneEdit(data, domain, owner, ipVersion)
	case constants.Custom:
		return newCustom(data, domain, owner, ipVersion)
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
}

func GetProviderInfo(name constants.Provider) ProviderInfo {
	info, ok := providerInfoMap[name]
	if !ok {
		return ProviderInfo{Name: name, Label: string(name)}
	}
	return info
}

func AllProviderInfos() []ProviderInfo {
	var infos []ProviderInfo
	for _, p := range constants.AllProviders() {
		infos = append(infos, GetProviderInfo(p))
	}
	return infos
}
