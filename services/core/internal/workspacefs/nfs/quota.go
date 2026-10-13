package nfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// The server command oac-workspace-quota (quota/oac-workspace-quota in this
// package) enforces XFS project quotas on the storage host. Its request,
// response and error codes are fixed by testdata/quota, shared by both tests.
const (
	inodesPerMiB    = 256
	quotaCallLimit  = 30 * time.Second
	quotaOutputSize = 64 << 10
)

type quotaParameters struct {
	Endpoint string `json:"endpoint"`
	User     string `json:"user"`
	HostKey  string `json:"host_key"`
}

type quotaCredential struct {
	SSHPrivateKey string `json:"ssh_private_key"`
}

var quotaUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// canonicalQuota validates the public quota parameters. The pinned host key is
// stored in authorized-key form; a comment is dropped and options are refused.
func canonicalQuota(raw json.RawMessage) (*quotaParameters, error) {
	invalid := fmt.Errorf("%w: quota endpoint, user or host_key", workspacefs.ErrInvalid)
	var q quotaParameters
	if strict(raw, &q) != nil || !quotaUser.MatchString(q.User) {
		return nil, invalid
	}
	host, port, err := net.SplitHostPort(q.Endpoint)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || number == 0 || host == "" || strings.ContainsAny(q.Endpoint, " \t\r\n\x00") {
		return nil, invalid
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(q.HostKey))
	if err != nil || len(options) > 0 || len(rest) > 0 {
		return nil, invalid
	}
	q.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	return &q, nil
}

// encodeCredential returns the canonical private credential: exactly one
// unencrypted SSH private key when quota is configured, otherwise nothing.
func encodeCredential(quota *quotaParameters, raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		if quota != nil {
			return nil, fmt.Errorf("%w: quota requires credential", workspacefs.ErrInvalid)
		}
		return nil, nil
	}
	var c quotaCredential
	if quota == nil || strict(raw, &c) != nil {
		return nil, fmt.Errorf("%w: credential", workspacefs.ErrInvalid)
	}
	if _, err := ssh.ParsePrivateKey([]byte(c.SSHPrivateKey)); err != nil {
		return nil, fmt.Errorf("%w: credential", workspacefs.ErrInvalid)
	}
	return json.Marshal(c)
}

type quotaClient struct {
	address string
	config  ssh.ClientConfig
}

func newQuotaClient(p quotaParameters, credential []byte) (*quotaClient, error) {
	var c quotaCredential
	if strict(credential, &c) != nil {
		return nil, fmt.Errorf("%w: credential", workspacefs.ErrInvalid)
	}
	signer, err := ssh.ParsePrivateKey([]byte(c.SSHPrivateKey))
	if err != nil {
		return nil, fmt.Errorf("%w: credential", workspacefs.ErrInvalid)
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return nil, fmt.Errorf("%w: host_key", workspacefs.ErrInvalid)
	}
	algorithms := []string{hostKey.Type()}
	if hostKey.Type() == ssh.KeyAlgoRSA {
		algorithms = []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return &quotaClient{address: p.Endpoint, config: ssh.ClientConfig{
		User: p.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey), HostKeyAlgorithms: algorithms, Timeout: quotaCallLimit,
	}}, nil
}

type quotaRequest struct {
	Operation       string `json:"operation"`
	NamespaceID     string `json:"namespace_id"`
	TenantID        string `json:"tenant_id,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	ObjectID        string `json:"object_id,omitempty"`
	ConfigurationID string `json:"configuration_id,omitempty"`
	CapacityMiB     uint32 `json:"capacity_mib,omitempty"`
	InodesHard      uint64 `json:"inodes_hard,omitempty"`
}

type quotaResponse struct {
	Error          string `json:"error,omitempty"`
	FilesystemUUID string `json:"filesystem_uuid,omitempty"`
	ProjectID      uint32 `json:"project_id,omitempty"`
	BytesHard      uint64 `json:"bytes_hard,omitempty"`
	InodesHard     uint64 `json:"inodes_hard,omitempty"`
}

// quotaErrors maps every helper error code to the protocol outcome.
var quotaErrors = map[string]error{
	"invalid_request":   workspacefs.ErrInvalid,
	"object_deleted":    workspacefs.ErrNotFound,
	"identity_mismatch": workspacefs.ErrOwnership,
	"binding_mismatch":  workspacefs.ErrOwnership,
	"object_replaced":   workspacefs.ErrOwnership,
	"foreign_project":   workspacefs.ErrOwnership,
	"nonempty_object":   workspacefs.ErrOwnership,
	"quota_mismatch":    workspacefs.ErrOwnership,
	"unsafe_path":       workspacefs.ErrOwnership,
	"object_missing":    workspacefs.ErrUnavailable,
	"enforcement_off":   workspacefs.ErrUnavailable,
	"range_exhausted":   workspacefs.ErrUnavailable,
	"journal_mismatch":  workspacefs.ErrUnavailable,
	"operation_failed":  workspacefs.ErrUnavailable,
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > quotaOutputSize {
		return 0, errors.New("helper output exceeds limit")
	}
	return b.Buffer.Write(p)
}

// call runs one helper request. Failure before the request is sent proves no
// mutation and is unavailable; afterwards a missing or malformed answer leaves
// the outcome unknown, which unconfirmed reports for mutating requests.
func (q *quotaClient) call(ctx context.Context, request quotaRequest) (quotaResponse, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return quotaResponse{}, err
	}
	unknown := workspacefs.ErrUnconfirmed
	if request.Operation == "check" {
		unknown = workspacefs.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, quotaCallLimit)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", q.address)
	if err != nil {
		return quotaResponse{}, unavailable(err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	config := q.config
	sshConn, channels, requests, err := ssh.NewClientConn(conn, q.address, &config)
	if err != nil {
		return quotaResponse{}, unavailable(err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return quotaResponse{}, unavailable(err)
	}
	defer session.Close()
	var output boundedBuffer
	session.Stdin, session.Stdout, session.Stderr = bytes.NewReader(raw), &output, io.Discard
	// The forced command ignores the requested command; the verb is in the request.
	runErr := session.Run("oac-workspace-quota")
	var response quotaResponse
	if strict(bytes.TrimSpace(output.Bytes()), &response) != nil {
		return quotaResponse{}, fmt.Errorf("%w: helper response: %v", unknown, runErr)
	}
	if response.Error != "" {
		mapped, ok := quotaErrors[response.Error]
		if !ok {
			return quotaResponse{}, fmt.Errorf("%w: helper error", unknown)
		}
		return quotaResponse{}, fmt.Errorf("%w: helper %s", mapped, response.Error)
	}
	if runErr != nil || !canonicalUUID(response.FilesystemUUID) {
		return quotaResponse{}, fmt.Errorf("%w: helper response", unknown)
	}
	return response, nil
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func (q *quotaClient) check(ctx context.Context, namespace string) error {
	response, err := q.call(ctx, quotaRequest{Operation: "check", NamespaceID: namespace})
	if err == nil && (response.ProjectID != 0 || response.BytesHard != 0 || response.InodesHard != 0) {
		err = fmt.Errorf("%w: helper check response", workspacefs.ErrUnavailable)
	}
	return err
}

// ensure binds the object's staging tree to its own project with exactly the
// admitted byte and inode hard limits, verified by the server before success.
func (q *quotaClient) ensure(ctx context.Context, ref workspacefs.Reference, configuration, namespace string, capacityMiB uint32) error {
	inodes := uint64(capacityMiB) * inodesPerMiB
	response, err := q.call(ctx, quotaRequest{Operation: "ensure", NamespaceID: namespace, TenantID: ref.TenantID, EnvironmentID: ref.EnvironmentID,
		ObjectID: ref.ObjectID, ConfigurationID: configuration, CapacityMiB: capacityMiB, InodesHard: inodes})
	if err != nil {
		return err
	}
	if response.ProjectID == 0 || response.BytesHard != uint64(capacityMiB)<<20 || response.InodesHard != inodes {
		return fmt.Errorf("%w: helper receipt differs from the admitted capacity", workspacefs.ErrOwnership)
	}
	return nil
}
