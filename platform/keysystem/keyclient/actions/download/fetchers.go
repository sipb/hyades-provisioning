package download

import (
	"fmt"
	"github.com/pkg/errors"

	"github.com/sipb/homeworld/platform/keysystem/api/endpoint"
	"github.com/sipb/homeworld/platform/keysystem/api/reqtarget"
	"github.com/sipb/homeworld/platform/keysystem/api/server"
	"github.com/sipb/homeworld/platform/keysystem/keyclient/state"
)

type DownloadFetcher interface {
	PrereqsSatisfied() error
	CanRetry() bool
	Fetch() ([]byte, error)
	Info() string
}

type AuthorityFetcher struct {
	Keyserver     *server.Keyserver
	AuthorityName string
}

type StaticFetcher struct {
	Keyserver  *server.Keyserver
	StaticName string
}

type APIFetcher struct {
	State *state.ClientState
	API   string
}

func (af *AuthorityFetcher) PrereqsSatisfied() error {
	return nil // so, yes
}

func (af *AuthorityFetcher) Info() string {
	return fmt.Sprintf("pubkey for authority %s", af.AuthorityName)
}

func (af *AuthorityFetcher) Fetch() ([]byte, error) {
	return af.Keyserver.GetPubkey(af.AuthorityName)
}

func (af *AuthorityFetcher) CanRetry() bool {
	return true
}

func (sf *StaticFetcher) PrereqsSatisfied() error {
	return nil // so, yes
}

func (sf *StaticFetcher) Info() string {
	return fmt.Sprintf("static file %s", sf.StaticName)
}

func (sf *StaticFetcher) Fetch() ([]byte, error) {
	return sf.Keyserver.GetStatic(sf.StaticName)
}

func (af *StaticFetcher) CanRetry() bool {
	return true
}

func (af *APIFetcher) PrereqsSatisfied() error {
	if af.State.Keygrant != nil {
		return nil
	} else {
		return errors.New("no keygranting certificate ready")
	}
}

func (af *APIFetcher) Info() string {
	return fmt.Sprintf("result from api %s", af.API)
}

func (af *APIFetcher) Fetch() ([]byte, error) {
	rt, err := af.State.Keyserver.AuthenticateWithCert(*af.State.Keygrant)
	if err != nil {
		return nil, err // no actual way for this part to fail
	}
	resp, err := reqtarget.SendRequest(rt, af.API, "")
	if err != nil {
		if _, is := errors.Cause(err).(endpoint.OperationForbidden); is {
			af.State.RetryFailed(af.API)
		}
		return nil, err
	}
	return []byte(resp), nil
}

func (af *APIFetcher) CanRetry() bool {
	return af.State.CanRetry(af.API)
}
