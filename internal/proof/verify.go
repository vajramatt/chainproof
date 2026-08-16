package proof

func VerifyBundle(bundle Bundle) Verification {
	if bundle.Format != "chainproof.bundle.v1" {
		return Verification{Valid: false, Reason: "unsupported_bundle_format"}
	}
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
		previous = stored
	}
	if len(bundle.Events) != bundle.Run.EntryCount {
		return Verification{Valid: false, Reason: "entry_count_mismatch", EntryCount: len(bundle.Events), ChainHead: previous}
	}
	if previous != bundle.Run.ChainHead {
		return Verification{Valid: false, Reason: "chain_head_mismatch", EntryCount: len(bundle.Events), ChainHead: previous}
	}
	return Verification{Valid: true, EntryCount: len(bundle.Events), ChainHead: previous}
}
