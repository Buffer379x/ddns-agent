package provider

import "ddns-agent/internal/provider/constants"

var providerInfoMap = map[constants.Provider]ProviderInfo{
	constants.Cloudflare: {
		Name: constants.Cloudflare, Label: "Cloudflare",
		Fields: []ProviderField{
			{Name: "zone_identifier", Label: "Zone ID", Type: "text", Required: true, Help: "Found in Cloudflare dashboard overview"},
			{Name: "token", Label: "API Token", Type: "password", Required: false, Help: "Scoped API token (recommended)"},
			{Name: "email", Label: "Email", Type: "email", Required: false, Help: "Global API key auth"},
			{Name: "key", Label: "API Key", Type: "password", Required: false, Help: "Global API key"},
			{Name: "proxied", Label: "Proxied", Type: "checkbox", Required: false},
			{Name: "ttl", Label: "TTL", Type: "number", Required: false, Placeholder: "1"},
		},
	},
	constants.DuckDNS: {
		Name: constants.DuckDNS, Label: "DuckDNS",
		Fields: []ProviderField{
			{Name: "token", Label: "Token", Type: "password", Required: true},
		},
	},
	constants.Namecheap: {
		Name: constants.Namecheap, Label: "Namecheap",
		Fields: []ProviderField{
			{Name: "password", Label: "Dynamic DNS Password", Type: "password", Required: true},
		},
	},
	constants.GoDaddy: {
		Name: constants.GoDaddy, Label: "GoDaddy",
		Fields: []ProviderField{
			{Name: "key", Label: "API Key", Type: "password", Required: true},
			{Name: "secret", Label: "API Secret", Type: "password", Required: true},
		},
	},
	constants.DigitalOcean: {
		Name: constants.DigitalOcean, Label: "DigitalOcean",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token", Type: "password", Required: true},
		},
	},
	constants.NoIP: {
		Name: constants.NoIP, Label: "No-IP",
		Fields: []ProviderField{
			{Name: "username", Label: "Username / Email", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Hetzner: {
		Name: constants.Hetzner, Label: "Hetzner",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token", Type: "password", Required: true},
			{Name: "zone_identifier", Label: "Zone ID", Type: "text", Required: false, Help: "Auto-detected if empty"},
		},
	},
	constants.Porkbun: {
		Name: constants.Porkbun, Label: "Porkbun",
		Fields: []ProviderField{
			{Name: "api_key", Label: "API Key", Type: "password", Required: true},
			{Name: "secret_api_key", Label: "Secret API Key", Type: "password", Required: true},
		},
	},
	constants.DeSEC: {
		Name: constants.DeSEC, Label: "deSEC",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token", Type: "password", Required: true},
		},
	},
	constants.OVH: {
		Name: constants.OVH, Label: "OVH",
		Fields: []ProviderField{
			{Name: "username", Label: "DynHost Username", Type: "text", Required: true},
			{Name: "password", Label: "DynHost Password", Type: "password", Required: true},
		},
	},
	constants.Strato: {
		Name: constants.Strato, Label: "Strato",
		Fields: []ProviderField{
			{Name: "password", Label: "DynDNS Password", Type: "password", Required: true},
		},
	},
	constants.INWX: {
		Name: constants.INWX, Label: "INWX",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Ionos: {
		Name: constants.Ionos, Label: "IONOS",
		Fields: []ProviderField{
			{Name: "api_key", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.Route53: {
		Name: constants.Route53, Label: "AWS Route 53",
		Fields: []ProviderField{
			{Name: "access_key", Label: "Access Key ID", Type: "text", Required: true},
			{Name: "secret_key", Label: "Secret Access Key", Type: "password", Required: true},
			{Name: "zone_id", Label: "Hosted Zone ID", Type: "text", Required: true},
			{Name: "ttl", Label: "TTL", Type: "number", Required: false, Placeholder: "300"},
		},
	},
	constants.Vultr: {
		Name: constants.Vultr, Label: "Vultr",
		Fields: []ProviderField{
			{Name: "token", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.Linode: {
		Name: constants.Linode, Label: "Linode",
		Fields: []ProviderField{
			{Name: "token", Label: "Personal Access Token", Type: "password", Required: true},
		},
	},
	constants.Gandi: {
		Name: constants.Gandi, Label: "Gandi",
		Fields: []ProviderField{
			{Name: "personal_access_token", Label: "Personal Access Token", Type: "password", Required: true},
		},
	},
	constants.Dynu: {
		Name: constants.Dynu, Label: "Dynu",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password / IP Update Password", Type: "password", Required: true},
		},
	},
	constants.DynV6: {
		Name: constants.DynV6, Label: "dynv6",
		Fields: []ProviderField{
			{Name: "token", Label: "Token", Type: "password", Required: true},
		},
	},
	constants.FreeDNS: {
		Name: constants.FreeDNS, Label: "FreeDNS",
		Fields: []ProviderField{
			{Name: "token", Label: "Update Token", Type: "password", Required: true},
		},
	},
	constants.EasyDNS: {
		Name: constants.EasyDNS, Label: "EasyDNS",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token", Type: "password", Required: true},
			{Name: "key", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.DNSOMatic: {
		Name: constants.DNSOMatic, Label: "DNS-O-Matic",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.DNSPod: {
		Name: constants.DNSPod, Label: "DNSPod",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token (ID,Token)", Type: "password", Required: true},
		},
	},
	constants.LuaDNS: {
		Name: constants.LuaDNS, Label: "LuaDNS",
		Fields: []ProviderField{
			{Name: "email", Label: "Email", Type: "email", Required: true},
			{Name: "token", Label: "API Token", Type: "password", Required: true},
		},
	},
	constants.HE: {
		Name: constants.HE, Label: "Hurricane Electric",
		Fields: []ProviderField{
			{Name: "password", Label: "DDNS Key", Type: "password", Required: true},
		},
	},
	constants.Njalla: {
		Name: constants.Njalla, Label: "Njalla",
		Fields: []ProviderField{
			{Name: "key", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.Infomaniak: {
		Name: constants.Infomaniak, Label: "Infomaniak",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.DDNSS: {
		Name: constants.DDNSS, Label: "DDNSS.de",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.NameSilo: {
		Name: constants.NameSilo, Label: "NameSilo",
		Fields: []ProviderField{
			{Name: "key", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.NameCom: {
		Name: constants.NameCom, Label: "Name.com",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "token", Label: "API Token", Type: "password", Required: true},
		},
	},
	constants.Dreamhost: {
		Name: constants.Dreamhost, Label: "Dreamhost",
		Fields: []ProviderField{
			{Name: "key", Label: "API Key", Type: "password", Required: true},
		},
	},
	constants.Domeneshop: {
		Name: constants.Domeneshop, Label: "Domeneshop",
		Fields: []ProviderField{
			{Name: "token", Label: "API Token", Type: "password", Required: true},
			{Name: "secret", Label: "API Secret", Type: "password", Required: true},
		},
	},
	constants.DonDominio: {
		Name: constants.DonDominio, Label: "DonDominio",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password / API Key", Type: "password", Required: true},
			{Name: "name", Label: "Service Name", Type: "text", Required: false},
		},
	},
	constants.Aliyun: {
		Name: constants.Aliyun, Label: "Alibaba Cloud (Aliyun)",
		Fields: []ProviderField{
			{Name: "access_key_id", Label: "Access Key ID", Type: "text", Required: true},
			{Name: "access_secret", Label: "Access Key Secret", Type: "password", Required: true},
		},
	},
	constants.AllInkl: {
		Name: constants.AllInkl, Label: "ALL-INKL",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.ChangeIP: {
		Name: constants.ChangeIP, Label: "ChangeIP",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.DD24: {
		Name: constants.DD24, Label: "DD24",
		Fields: []ProviderField{
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Dyn: {
		Name: constants.Dyn, Label: "Dyn",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password / Client Key", Type: "password", Required: true},
		},
	},
	constants.GoIP: {
		Name: constants.GoIP, Label: "GoIP.de",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.GCP: {
		Name: constants.GCP, Label: "Google Cloud DNS",
		Fields: []ProviderField{
			{Name: "project", Label: "Project ID", Type: "text", Required: true},
			{Name: "zone", Label: "Managed Zone Name", Type: "text", Required: true},
			{Name: "credentials", Label: "Service Account JSON", Type: "textarea", Required: true},
		},
	},
	constants.Loopia: {
		Name: constants.Loopia, Label: "Loopia",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.MyAddr: {
		Name: constants.MyAddr, Label: "myaddr.tools",
		Fields: []ProviderField{
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Netcup: {
		Name: constants.Netcup, Label: "Netcup",
		Fields: []ProviderField{
			{Name: "customer_number", Label: "Customer Number", Type: "text", Required: true},
			{Name: "api_key", Label: "API Key", Type: "password", Required: true},
			{Name: "api_password", Label: "API Password", Type: "password", Required: true},
		},
	},
	constants.NowDNS: {
		Name: constants.NowDNS, Label: "Now-DNS",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.OpenDNS: {
		Name: constants.OpenDNS, Label: "OpenDNS",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.SelfhostDe: {
		Name: constants.SelfhostDe, Label: "Selfhost.de",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Servercow: {
		Name: constants.Servercow, Label: "Servercow",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "password", Label: "Password", Type: "password", Required: true},
		},
	},
	constants.Spdyn: {
		Name: constants.Spdyn, Label: "spdyn.de",
		Fields: []ProviderField{
			{Name: "username", Label: "Username / Hostname", Type: "text", Required: true},
			{Name: "password", Label: "Update Token", Type: "password", Required: true},
		},
	},
	constants.Variomedia: {
		Name: constants.Variomedia, Label: "Variomedia",
		Fields: []ProviderField{
			{Name: "email", Label: "Email", Type: "email", Required: true},
			{Name: "password", Label: "API Token", Type: "password", Required: true},
		},
	},
	constants.ZoneEdit: {
		Name: constants.ZoneEdit, Label: "ZoneEdit",
		Fields: []ProviderField{
			{Name: "username", Label: "Username", Type: "text", Required: true},
			{Name: "token", Label: "Token", Type: "password", Required: true},
		},
	},
	constants.Custom: {
		Name: constants.Custom, Label: "Custom URL",
		Fields: []ProviderField{
			{Name: "url", Label: "Update URL", Type: "text", Required: true, Help: "Use {ip} and {domain} as placeholders"},
		},
	},
}
