package constants

type Provider string

const (
	Aliyun       Provider = "aliyun"
	AllInkl      Provider = "allinkl"
	ChangeIP     Provider = "changeip"
	Cloudflare   Provider = "cloudflare"
	Custom       Provider = "custom"
	DD24         Provider = "dd24"
	DDNSS        Provider = "ddnss"
	DeSEC        Provider = "desec"
	DigitalOcean Provider = "digitalocean"
	DNSOMatic    Provider = "dnsomatic"
	DNSPod       Provider = "dnspod"
	Domeneshop   Provider = "domeneshop"
	DonDominio   Provider = "dondominio"
	Dreamhost    Provider = "dreamhost"
	DuckDNS      Provider = "duckdns"
	Dyn          Provider = "dyn"
	Dynu         Provider = "dynu"
	DynV6        Provider = "dynv6"
	EasyDNS      Provider = "easydns"
	FreeDNS      Provider = "freedns"
	Gandi        Provider = "gandi"
	GCP          Provider = "gcp"
	GoDaddy      Provider = "godaddy"
	GoIP         Provider = "goip"
	HE           Provider = "he"
	Hetzner      Provider = "hetzner"
	Infomaniak   Provider = "infomaniak"
	INWX         Provider = "inwx"
	Ionos        Provider = "ionos"
	Linode       Provider = "linode"
	Loopia       Provider = "loopia"
	LuaDNS       Provider = "luadns"
	MyAddr       Provider = "myaddr"
	Namecheap    Provider = "namecheap"
	NameCom      Provider = "name.com"
	NameSilo     Provider = "namesilo"
	Netcup       Provider = "netcup"
	Njalla       Provider = "njalla"
	NoIP         Provider = "noip"
	NowDNS       Provider = "nowdns"
	OpenDNS      Provider = "opendns"
	OVH          Provider = "ovh"
	Porkbun      Provider = "porkbun"
	Route53      Provider = "route53"
	SelfhostDe   Provider = "selfhost.de"
	Servercow    Provider = "servercow"
	Spdyn        Provider = "spdyn"
	Strato       Provider = "strato"
	Variomedia   Provider = "variomedia"
	Vultr        Provider = "vultr"
	ZoneEdit     Provider = "zoneedit"
)

func AllProviders() []Provider {
	return []Provider{
		Aliyun, AllInkl, ChangeIP, Cloudflare, Custom, DD24, DDNSS, DeSEC,
		DigitalOcean, DNSOMatic, DNSPod, Domeneshop, DonDominio, Dreamhost,
		DuckDNS, Dyn, Dynu, DynV6, EasyDNS, FreeDNS, Gandi, GCP, GoDaddy,
		GoIP, HE, Hetzner, Infomaniak, INWX, Ionos, Linode, Loopia, LuaDNS,
		MyAddr, Namecheap, NameCom, NameSilo, Netcup, Njalla, NoIP, NowDNS,
		OpenDNS, OVH, Porkbun, Route53, SelfhostDe, Servercow, Spdyn, Strato,
		Variomedia, Vultr, ZoneEdit,
	}
}

type IPVersion string

const (
	IPv4 IPVersion = "ipv4"
	IPv6 IPVersion = "ipv6"
	Dual IPVersion = "dual"
)

const (
	A    = "A"
	AAAA = "AAAA"
)
