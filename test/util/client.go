package util

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	g "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	e2e "k8s.io/kubernetes/test/e2e/framework"
	e2edebug "k8s.io/kubernetes/test/e2e/framework/debug"
)

// Exec executes a command and returns its output.
func Exec(command string, args ...string) (string, error) {
	cmd := exec.Command(command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command %q %q failed with exit code %d: %s\n%s", command, args, cmd.ProcessState.Sys().(syscall.WaitStatus).ExitStatus(), stderr.String(), err)
	}
	return stdout.String(), nil
}

// MustExec executes a command and panics if it fails.
func MustExec(command string, args ...string) string {
	out, err := Exec(command, args...)
	if err != nil {
		panic(err)
	}
	return out
}

// GetArgumentForQuote gets the argument for quote.
func GetArgumentForQuote(args ...string) string {
	var newArgs []string
	for _, arg := range args {
		if strings.Contains(arg, " ") {
			newArgs = append(newArgs, "'"+arg+"'")
		} else {
			newArgs = append(newArgs, arg)
		}
	}
	return strings.Join(newArgs, " ")
}

// GetKubeconfigPath returns the path to the kubeconfig file.
func GetKubeconfigPath() string {
	return MustExec("mktemp", "-u", "/tmp/kubeconfig.XXXXXX")
}

// Run executes a command and returns the exit code.
func Run(command string, args ...string) (int, error) {
	cmd := exec.Command(command, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), err
		}
		return 0, err
	}
	return 0, nil
}

// RunInBackgroud executes a command in the background.
func RunInBackgroud(command string, args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer, error) {
	cmd := exec.Command(command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Start()
	return cmd, &stdout, &stderr, err
}

// WaitUntilRun executes a command until it succeeds.
func WaitUntilRun(command string, args ...string) (string, error) {
	var (
		output string
		err    error
	)
	if err = wait.PollUntilContextTimeout(context.Background(), 5*time.Second, 5*time.Second, true, func(context.Context) (bool, error) {
		output, err = Exec(command, args...)
		if err != nil {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return output, fmt.Errorf("command %q %q failed with error: %v", command, args, err)
	}
	return output, nil
}

// CLI provides function to call the OpenShift CLI and Kubernetes and OpenShift
// clients.
type CLI struct {
	execPath        string
	verb            string
	configPath      string
	guestConfigPath string
	adminConfigPath string
	username        string
	globalArgs      []string
	commandArgs     []string
	finalArgs       []string
	//namespacesToDelete []string
	stdin            *bytes.Buffer
	stdout           io.Writer
	stderr           io.Writer
	verbose          bool
	showInfo         bool
	withoutNamespace bool
	withoutKubeconf  bool
	asGuestKubeconf  bool
	kubeFramework    *e2e.Framework

	resourcesToDelete []resourceRef
	pathsToDelete     []string
}

type resourceRef struct {
	Resource  schema.GroupVersionResource
	Namespace string
	Name      string
}

// NewCLIWithoutNamespace initialize the upstream E2E framework without adding a
// namespace. You may call SetupProject() to create one.
func NewCLIWithoutNamespace(project string) *CLI {
	client := &CLI{}

	// must be registered before framework initialization which registers other Ginkgo setup nodes
	g.BeforeEach(func() { SkipOnOpenShiftNess(true) })

	// must be registered before the e2e framework aftereach
	g.AfterEach(client.TeardownProject)

	client.kubeFramework = e2e.NewDefaultFramework(project)
	client.kubeFramework.SkipNamespaceCreation = true
	client.username = "admin"
	client.execPath = "oc"
	client.adminConfigPath = KubeConfigPath()
	client.showInfo = true
	return client
}

// AsAdmin changes current config file path to the admin config.
func (c *CLI) AsAdmin() *CLI {
	nc := *c
	nc.configPath = c.adminConfigPath
	return &nc
}

// WithoutNamespace instructs the command should be invoked without adding --namespace parameter
func (c CLI) WithoutNamespace() *CLI {
	c.withoutNamespace = true
	return &c
}

// Run executes given OpenShift CLI command verb (iow. "oc <verb>").
// This function also override the default 'stdout' to redirect all output
// to a buffer and prepare the global flags such as namespace and config path.
func (c *CLI) Run(commands ...string) *CLI {
	in, out, errout := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	nc := &CLI{
		execPath:        c.execPath,
		verb:            commands[0],
		kubeFramework:   c.KubeFramework(),
		adminConfigPath: c.adminConfigPath,
		configPath:      c.configPath,
		showInfo:        c.showInfo,
		guestConfigPath: c.guestConfigPath,
		username:        c.username,
		globalArgs:      commands,
	}
	if !c.withoutKubeconf {
		if c.asGuestKubeconf {
			if c.guestConfigPath != "" {
				nc.globalArgs = append([]string{fmt.Sprintf("--kubeconfig=%s", c.guestConfigPath)}, nc.globalArgs...)
			} else {
				FatalErr("want to use guest cluster kubeconfig, but it is not set, so please use oc.SetGuestKubeconf to set it firstly")
			}
		} else {
			nc.globalArgs = append([]string{fmt.Sprintf("--kubeconfig=%s", c.configPath)}, nc.globalArgs...)
		}
	}
	if c.asGuestKubeconf && !c.withoutNamespace {
		FatalErr("you are doing something in ns of guest cluster, please use WithoutNamespace and set ns in Args, for example, oc.AsGuestKubeconf().WithoutNamespace().Run(\"get\").Args(\"pods\", \"-n\", \"guestclusterns\").Output()")
	}
	if !c.withoutNamespace {
		nc.globalArgs = append([]string{fmt.Sprintf("--namespace=%s", c.Namespace())}, nc.globalArgs...)
	}
	nc.stdin, nc.stdout, nc.stderr = in, out, errout
	return nc.setOutput(c.stdout)
}

// Args sets the additional arguments for the OpenShift CLI command
func (c *CLI) Args(args ...string) *CLI {
	c.commandArgs = args
	c.finalArgs = append(c.globalArgs, c.commandArgs...)
	return c
}

// Output executes the command and returns stdout/stderr combined into one string
func (c *CLI) Output() (string, error) {
	if c.verbose {
		fmt.Printf("DEBUG: oc %s\n", c.printCmd())
	}
	cmd := exec.Command(c.execPath, c.finalArgs...)
	cmd.Stdin = c.stdin
	if c.showInfo {
		e2e.Logf("Running '%s %s'", c.execPath, strings.Join(c.finalArgs, " "))
	}
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	switch err := err.(type) {
	case nil:
		c.stdout = bytes.NewBuffer(out)
		return trimmed, nil
	case *exec.ExitError:
		e2e.Logf("Error running %v:\n%s", cmd, trimmed)
		return trimmed, &ExitError{ExitError: err, Cmd: c.execPath + " " + strings.Join(c.finalArgs, " "), StdErr: trimmed}
	default:
		FatalErr(fmt.Errorf("unable to execute %q: %v", c.execPath, err))
		// unreachable code
		return "", nil
	}
}

// Execute executes the current command and return error if the execution failed
// This function will set the default output to Ginkgo writer.
func (c *CLI) Execute() error {
	out, err := c.Output()
	if _, err := io.Copy(g.GinkgoWriter, strings.NewReader(out+"\n")); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Unable to copy the output to ginkgo writer")
	}
	os.Stdout.Sync()
	return err
}

// KubeFramework returns Kubernetes framework which contains helper functions
// specific for Kubernetes resources
func (c *CLI) KubeFramework() *e2e.Framework {
	return c.kubeFramework
}

// FatalErr exits the test in case a fatal error has occurred.
func FatalErr(msg interface{}) {
	// the path that leads to this being called isn't always clear...
	fmt.Fprintln(g.GinkgoWriter, string(debug.Stack()))
	e2e.Failf("%v", msg)
}

// Namespace returns the name of the namespace used in the current test case.
// If the namespace is not set, an empty string is returned.
func (c *CLI) Namespace() string {
	if c.kubeFramework.Namespace == nil {
		return ""
	}
	return c.kubeFramework.Namespace.Name
}

// setOutput allows to override the default command output
func (c *CLI) setOutput(out io.Writer) *CLI {
	c.stdout = out
	return c
}

func (c *CLI) printCmd() string {
	return strings.Join(c.finalArgs, " ")
}

// ExitError struct
type ExitError struct {
	Cmd    string
	StdErr string
	*exec.ExitError
}

// TeardownProject removes projects created by this test.
func (c *CLI) TeardownProject() {
	e2e.TestContext.DumpLogsOnFailure = os.Getenv("DUMP_EVENTS_ON_FAILURE") != "false"
	if len(c.Namespace()) > 0 && g.CurrentSpecReport().Failed() && e2e.TestContext.DumpLogsOnFailure {
		e2edebug.DumpAllNamespaceInfo(context.TODO(), c.kubeFramework.ClientSet, c.Namespace())
	}

	if len(c.configPath) > 0 {
		os.Remove(c.configPath)
	}

	dynamicClient := c.AdminDynamicClient()
	for _, resource := range c.resourcesToDelete {
		err := dynamicClient.Resource(resource.Resource).Namespace(resource.Namespace).Delete(context.Background(), resource.Name, metav1.DeleteOptions{})
		e2e.Logf("Deleted %v, err: %v", resource, err)
	}
	for _, path := range c.pathsToDelete {
		err := os.RemoveAll(path)
		e2e.Logf("Deleted path %v, err: %v", path, err)
	}
}

// AdminDynamicClient method
func (c *CLI) AdminDynamicClient() dynamic.Interface {
	return dynamic.NewForConfigOrDie(c.AdminConfig())
}

// AdminConfig method
func (c *CLI) AdminConfig() *rest.Config {
	clientConfig, err := getClientConfig(c.adminConfigPath)
	if err != nil {
		FatalErr(err)
	}
	return clientConfig
}

func getClientConfig(kubeConfigFile string) (*rest.Config, error) {
	kubeConfigBytes, err := os.ReadFile(kubeConfigFile)
	if err != nil {
		return nil, err
	}
	kubeConfig, err := clientcmd.NewClientConfigFromBytes(kubeConfigBytes)
	if err != nil {
		return nil, err
	}
	clientConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	clientConfig.WrapTransport = defaultClientTransport

	return clientConfig, nil
}

// defaultClientTransport sets defaults for a client Transport that are suitable
// for use by infrastructure components.
func defaultClientTransport(rt http.RoundTripper) http.RoundTripper {
	transport, ok := rt.(*http.Transport)
	if !ok {
		return rt
	}

	// TODO: this should be configured by the caller, not in this method.
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport.DialContext = dialer.DialContext

	// Hold open more internal idle connections
	// TODO: this should be configured by the caller, not in this method.
	transport.MaxIdleConnsPerHost = 100
	return transport
}

// AdminKubeClient provides a Kubernetes client for the cluster admin user.
func (c *CLI) AdminKubeClient() kubernetes.Interface {
	return kubernetes.NewForConfigOrDie(c.AdminConfig())
}
