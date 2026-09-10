package proof

import "github.com/vajramatt/chainproof/internal/identity"

func VerifyBundle(bundle Bundle) Verification {
	if bundle.Format != "chainproof.bundle.v1" {
		return Verification{Valid: false, Reason: "unsupported_bundle_format"}
	}
	runIdentity, runHasIdentity := bundle.Run.Metadata[identity.ExtensionKey]
	if runHasIdentity && identity.ValidateExtension(runIdentity) != nil {
		return Verification{Valid: false, Reason: "invalid_agent_identity"}
	}
	sawIdentityBinding := false
	previous := GenesisHash
	for i, event := range bundle.Events {
		sequence := i
		if event.RunID != bundle.Run.ID {
			return Verification{Valid: false, Reason: "run_id_mismatch", Sequence: &sequence}
		}
		if event.Sequence != i {
			return Verification{Valid: false, Reason: "sequence_gap", Sequence: &sequence}
		}
		if event.PreviousHash != previous {
			return Verification{Valid: false, Reason: "previous_hash_mismatch", Sequence: &sequence}
		}
		stored := event.EventHash
		event.EventHash = ""
		computed, err := Hash(event)
		if err != nil || computed != stored {
			return Verification{Valid: false, Reason: "event_hash_mismatch", Sequence: &sequence}
		}
		if eventIdentity, hasIdentity := event.Extensions[identity.ExtensionKey]; hasIdentity {
			sawIdentityBinding = true
			if !runHasIdentity {
				return Verification{Valid: false, Reason: "agent_identity_metadata_missing", Sequence: &sequence}
			}
			runRaw, runErr := CanonicalJSON(runIdentity)
			eventRaw, eventErr := CanonicalJSON(eventIdentity)
			if runErr != nil || eventErr != nil || string(runRaw) != string(eventRaw) {
				return Verification{Valid: false, Reason: "agent_identity_mismatch", Sequence: &sequence}
			}
		}
		previous = stored
	}
	if runHasIdentity && len(bundle.Events) > 0 && !sawIdentityBinding {
		return Verification{Valid: false, Reason: "agent_identity_binding_missing", EntryCount: len(bundle.Events), ChainHead: previous}
	}
	if len(bundle.Events) != bundle.Run.EntryCount {
		return Verification{Valid: false, Reason: "entry_count_mismatch", EntryCount: len(bundle.Events), ChainHead: previous}
	}
	if previous != bundle.Run.ChainHead {
		return Verification{Valid: false, Reason: "chain_head_mismatch", EntryCount: len(bundle.Events), ChainHead: previous}
	}
	return Verification{Valid: true, EntryCount: len(bundle.Events), ChainHead: previous}
}
