package worldconfig

import (
	"fmt"
	"os"
	"time"

	"github.com/sipb/homeworld/platform/keysystem/keyserver/account"
	"github.com/sipb/homeworld/platform/keysystem/keyserver/authorities"
	"github.com/sipb/homeworld/platform/keysystem/keyserver/config"
	"github.com/sipb/homeworld/platform/keysystem/keyserver/verifier"
	"github.com/sipb/homeworld/platform/keysystem/worldconfig/paths"
)

type Groups struct {
	KerberosAccounts *account.Group
	Nodes            *account.Group
}

func GenerateAccounts(context *config.Context, conf *SpireSetup, auth Authorities) {
	var accounts []*account.Account

	groups := Groups{
		KerberosAccounts: &account.Group{},
		Nodes:            &account.Group{},
	}

	// TODO: ensure that node hostnames are not duplicated

	for _, node := range conf.Nodes {
		acc := &account.Account{
			Principal: node.Hostname + "." + conf.Cluster.ExternalDomain,
			LimitIP:   node.NetIP(),
		}
		accounts = append(accounts, acc)

		groups.Nodes.AllMembers = append(groups.Nodes.AllMembers, acc)
		acc.Privileges = GrantsForNodeAccount(context, conf, groups, auth, acc, node)
	}

	// metrics principal used by homeworld-ssh-checker
	allAdmins := append([]string{"metrics@NONEXISTENT.REALM.INVALID"}, conf.RootAdmins...)

	for _, rootAdmin := range allAdmins {
		// TODO: ensure that root admins are unique, including against the metrics admin
		acc := &account.Account{
			Principal:         rootAdmin,
			DisableDirectAuth: true,
		}
		accounts = append(accounts, acc)
		groups.KerberosAccounts.AllMembers = append(groups.KerberosAccounts.AllMembers, acc)
		acc.Privileges = GrantsForRootAdminAccount(context, groups, auth, acc)
	}

	// if we don't have any root admins, this means that kerberos authentication is disabled, and we shouldn't add this
	// service account, which is only used by auth-monitor for verifying the keygateway's functionality.
	if len(conf.RootAdmins) > 0 {
		for _, node := range conf.Nodes {
			if node.Kind == "supervisor" {
				// auth-monitor will authenticate as this principal, because it's the only keytab we have in the system
				principal := "host/" + node.Hostname + "." + conf.Cluster.ExternalDomain + "@" + conf.Cluster.KerberosRealm
				acc := &account.Account{
					Principal:         principal,
					DisableDirectAuth: true,
				}
				accounts = append(accounts, acc)
				groups.KerberosAccounts.AllMembers = append(groups.KerberosAccounts.AllMembers, acc)
				// no privileges needed for this. it's just used to test that kerberos auth works correctly.
				acc.Privileges = map[string]account.Privilege{}
			}
		}
	}

	for _, ac := range accounts {
		context.Accounts[ac.Principal] = ac
	}
}

type Authorities struct {
	Keygranting    *authorities.TLSAuthority
	ServerTLS      *authorities.TLSAuthority
	ClusterTLS     *authorities.TLSAuthority
	SshUser        *authorities.SSHAuthority
	SshHost        *authorities.SSHAuthority
	EtcdServer     *authorities.TLSAuthority
	EtcdClient     *authorities.TLSAuthority
	Kubernetes     *authorities.TLSAuthority
	ServiceAccount *authorities.StaticAuthority
}

func GenerateAuthorities(conf *SpireSetup) map[string]config.ConfigAuthority {
	var presentAs []string
	for _, node := range conf.Nodes {
		if node.Kind == "supervisor" {
			presentAs = append(presentAs, node.Hostname+"."+conf.Cluster.ExternalDomain)
		}
	}

	return map[string]config.ConfigAuthority{
		AuthenticationAuthority: {
			Type: "TLS",
			Key:  "keygrant.key",
			Cert: "keygrant.pem",
		},
		ServerTLS: {
			Type:      "TLS",
			Key:       "server.key",
			Cert:      "server.pem",
			PresentAs: presentAs,
		},
		"clustertls": {
			Type: "TLS",
			Key:  "cluster.key",
			Cert: "cluster.cert",
		},
		"ssh-user": {
			Type: "SSH",
			Key:  "ssh_user_ca",
			Cert: "ssh_user_ca.pub",
		},
		"ssh-host": {
			Type: "SSH",
			Key:  "ssh_host_ca",
			Cert: "ssh_host_ca.pub",
		},
		"etcd-server": {
			Type: "TLS",
			Key:  "etcd-server.key",
			Cert: "etcd-server.pem",
		},
		"etcd-client": {
			Type: "TLS",
			Key:  "etcd-client.key",
			Cert: "etcd-client.pem",
		},
		"kubernetes": {
			Type: "TLS",
			Key:  "kubernetes.key",
			Cert: "kubernetes.pem",
		},
		"serviceaccount": {
			Type: "static",
			Key:  "serviceaccount.key",
			Cert: "serviceaccount.pem",
		},
	}
}

func GrantsForRootAdminAccount(c *config.Context, groups Groups, auth Authorities, ac *account.Account) map[string]account.Privilege {
	var grants = map[string]account.Privilege{}

	// ADMIN ACCESS TO THE RUNNING CLUSTER

	grants["access-ssh"] = account.NewSSHGrantPrivilege(
		auth.SshUser, false, 4*time.Hour,
		"temporary-ssh-grant-"+ac.Principal, []string{"root"},
	)
	grants["access-etcd"] = account.NewTLSGrantPrivilege(
		auth.EtcdClient, false, 4*time.Hour,
		"temporary-etcd-grant-"+ac.Principal, nil,
	)
	grants["access-kubernetes"] = account.NewTLSGrantPrivilege(
		auth.Kubernetes, false, 4*time.Hour,
		"temporary-kube-grant-"+ac.Principal, nil,
	)

	// MEMBERSHIP IN THE CLUSTER

	grants["bootstrap"] = account.NewBootstrapPrivilege(groups.Nodes, time.Hour, c.TokenVerifier.Registry)

	return grants
}

func GenerateLocalConf(conf *SpireSetup, node *SpireNode) string {
	var schedule string
	if node.Kind == "worker" {
		schedule = "true"
	} else if node.Kind == "master" || node.Kind == "supervisor" {
		schedule = "false"
	} else {
		panic("invalid node Kind")
	}

	return `# generated automatically by keyserver
HOST_NODE=` + node.Hostname + `
HOST_DNS=` + node.Hostname + `.` + conf.Cluster.ExternalDomain + `
HOST_IP=` + node.IP + `
SCHEDULE_WORK=` + schedule + `
KIND=` + node.Kind
}

func GrantsForNodeAccount(c *config.Context, conf *SpireSetup, groups Groups, auth Authorities, ac *account.Account, node *SpireNode) map[string]account.Privilege {
	// NOTE: at the point where this runs, not all accounts will necessarily be registered with the context!
	var grants = map[string]account.Privilege{}

	// MEMBERSHIP IN THE CLUSTER

	if node.Kind == "supervisor" {
		grants["bootstrap-keyinit"] = account.NewBootstrapPrivilege(groups.Nodes, time.Hour, c.TokenVerifier.Registry)
		grants["auth-to-kerberos"] = account.NewImpersonatePrivilege(c.GetAccount, groups.KerberosAccounts)
	}

	grants["renew-keygrant"] = account.NewTLSGrantPrivilege(auth.Keygranting, false, OneDay*40, ac.Principal, nil)

	// CONFIGURATION ENDPOINT

	grants["get-local-config"] = account.NewConfigurationPrivilege(GenerateLocalConf(conf, node))

	// SERVER CERTIFICATES

	grants["grant-ssh-host"] = account.NewSSHGrantPrivilege(
		auth.SshHost, true, OneDay*60, "admitted-"+ac.Principal,
		[]string{
			node.Hostname + "." + conf.Cluster.ExternalDomain,
			node.Hostname,
			node.IP,
		},
	)

	if node.Kind == "master" {
		grants["grant-kubernetes-master"] = account.NewTLSGrantPrivilege(
			auth.Kubernetes, true, 30*OneDay, "kube-master-"+node.Hostname,
			[]string{
				node.Hostname + "." + conf.Cluster.ExternalDomain,
				node.Hostname,
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.svc",
				"kubernetes.default.svc." + conf.Cluster.InternalDomain,
				node.IP,
				conf.Addresses.ServiceAPI,
			},
		)
		grants["grant-etcd-server"] = account.NewTLSGrantPrivilege(
			auth.EtcdServer, true, 30*OneDay, "etcd-server-"+node.Hostname,
			[]string{
				node.Hostname + "." + conf.Cluster.ExternalDomain,
				node.Hostname,
				node.IP,
			},
		)
	}

	if node.Kind == "supervisor" {
		grants["grant-registry-host"] = account.NewTLSGrantPrivilege(
			auth.ClusterTLS, true, 30*OneDay, "homeworld-supervisor-"+node.Hostname,
			[]string{"homeworld.private"},
		)
	}

	// CLIENT CERTIFICATES

	grants["grant-kubernetes-worker"] = account.NewTLSGrantPrivilege(
		auth.Kubernetes, true, 30*OneDay, "kube-worker-"+node.Hostname,
		[]string{
			node.Hostname + "." + conf.Cluster.ExternalDomain,
			node.Hostname,
			node.IP,
		},
	)

	if node.Kind == "master" {
		grants["grant-etcd-client"] = account.NewTLSGrantPrivilege(auth.EtcdClient, false, 30*OneDay, "etcd-client-"+node.Hostname,
			[]string{
				node.Hostname + "." + conf.Cluster.ExternalDomain,
				node.Hostname,
				node.IP,
			},
		)
		grants["fetch-serviceaccount-key"] = account.NewFetchKeyPrivilege(auth.ServiceAccount)
	}

	return grants
}

func ValidateStaticFiles(context *config.Context) error {
	for _, static := range context.StaticFiles {
		// check for existence
		info, err := os.Stat(static.Filepath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("static file at %s is directory", static.Filepath)
		}
	}
	return nil
}

const AuthorityKeyDirectory = "/etc/homeworld/keyserver/authorities/"
const ClusterConfigPath = "/etc/homeworld/keyserver/static/cluster.conf"
const MachineListPath = "/etc/homeworld/keyserver/static/machine.list"
const AuthenticationAuthority = "keygranting"
const ServerTLS = "servertls"

func GenerateConfig() (*config.Context, error) {
	conf, err := LoadSpireSetup(paths.SpireSetupPath)
	if err != nil {
		return nil, err
	}

	context := &config.Context{
		TokenVerifier: verifier.NewTokenVerifier(),
		StaticFiles: map[string]config.StaticFile{
			"cluster.conf": {
				Filename: "cluster.conf",
				Filepath: ClusterConfigPath,
			},
			"machine.list": {
				Filename: "machine.list",
				Filepath: MachineListPath,
			},
		},
		Authorities: map[string]authorities.Authority{},
		Accounts:    map[string]*account.Account{},
	}
	err = ValidateStaticFiles(context)
	if err != nil {
		return nil, err
	}
	for name, authority := range GenerateAuthorities(conf) {
		loaded, err := authority.Load(AuthorityKeyDirectory)
		if err != nil {
			return nil, err
		}
		context.Authorities[name] = loaded
	}
	auth := Authorities{
		Keygranting:    context.Authorities[AuthenticationAuthority].(*authorities.TLSAuthority),
		ServerTLS:      context.Authorities[ServerTLS].(*authorities.TLSAuthority),
		ClusterTLS:     context.Authorities["clustertls"].(*authorities.TLSAuthority),
		EtcdClient:     context.Authorities["etcd-client"].(*authorities.TLSAuthority),
		EtcdServer:     context.Authorities["etcd-server"].(*authorities.TLSAuthority),
		Kubernetes:     context.Authorities["kubernetes"].(*authorities.TLSAuthority),
		ServiceAccount: context.Authorities["serviceaccount"].(*authorities.StaticAuthority),
		SshHost:        context.Authorities["ssh-host"].(*authorities.SSHAuthority),
		SshUser:        context.Authorities["ssh-user"].(*authorities.SSHAuthority),
	}
	context.AuthenticationAuthority = auth.Keygranting
	context.ServerTLS = auth.ServerTLS
	GenerateAccounts(context, conf, auth)
	return context, nil
}
