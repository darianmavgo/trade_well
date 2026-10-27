// Package modelstate keeps the work the trained models do each day in the orchestrator
// database (Turso), so a cold App Engine instance knows which sessions are already done and
// downloads or computes only what is missing: the hidden Markov and Markov models' daily
// predictions, and the long daily bar history the hidden Markov model is extended from.
//
// The hmm and markov strategies extend a saved model (hmm_models.db, markov_models.db) with one
// prediction per new session, and that file lives in /tmp, so every cold start begins from the
// bundled copy and every instance would extend it again from the same point. Before the scan,
// HydrateHMM and HydrateMarkov copy the predictions stored in Turso into the local file and move
// the model's last_date forward; the strategy's Prepare then finds nothing left to do, or only
// the sessions nobody has predicted yet. After the scan, PersistHMM and PersistMarkov store what
// the instance added.
//
// Rows are keyed by the model's trained_at, so a retrained model never reads the old model's
// predictions. A model's parameters are frozen after training, which makes a stored prediction
// exactly what any instance would compute for that session.
package modelstate
