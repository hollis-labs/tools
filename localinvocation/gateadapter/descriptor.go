// Package gateadapter binds one exact prepared local invocation to an
// operation-bound approval. It does not execute commands or issue authority.
package gateadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

const DescriptorVersion = "local-invocation-v2"

// Descriptor holds only the prepared command facts. Gate references are
// deliberately excluded: they are produced after this operation is hashed.
// Environment contents stay behind the host-issued reference.
type Descriptor struct {
	Argv           []string              `json:"argv"`
	SourceForm     invocation.SourceForm `json:"source_form"`
	CWD            string                `json:"cwd"`
	EnvironmentRef string                `json:"environment_ref,omitempty"`
	IDs            invocation.IDs        `json:"ids"`
	Runner         runner.Options        `json:"runner"`
}

func Describe(command invocation.Command, options runner.Options) (Descriptor, error) {
	if len(command.Argv) == 0 || command.Argv[0] == "" || !strings.HasPrefix(command.CWD, "/") || command.IDs.CallID == "" || command.IDs.AttemptID == "" || command.IDs.Attempt == 0 || (command.SourceForm != invocation.FormArgv && command.SourceForm != invocation.FormShellCommand) {
		return Descriptor{}, errors.New("gateadapter: invalid prepared command")
	}
	return Descriptor{Argv: append([]string(nil), command.Argv...), SourceForm: command.SourceForm, CWD: command.CWD, EnvironmentRef: command.EnvironmentRef, IDs: command.IDs, Runner: options}, nil
}

// Digest is the exact command hash the host must recompute at admission. It
// includes argv, cwd, environment reference and invocation/attempt identity.
func Digest(command invocation.Command, options runner.Options) (runtimecontract.Digest, error) {
	descriptor, err := Describe(command, options)
	if err != nil {
		return runtimecontract.Digest{}, err
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return runtimecontract.Digest{}, err
	}
	sum := sha256.Sum256(encoded)
	return runtimecontract.Digest{Scheme: "sha256", DescriptorVersion: DescriptorVersion, Value: hex.EncodeToString(sum[:])}, nil
}
