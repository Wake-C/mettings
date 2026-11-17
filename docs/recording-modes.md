# Recording and transcription

Mettings owns two independent activities: capture saves audio, and transcription
turns saved audio into versioned text. A failed or slow ASR job never stops capture.

## User controls

Meeting mode creates a session folder immediately and records until Finish.
Finish always saves audio. Auto-transcribe is a visible, remembered per-mode
choice, off by default. File and URL imports follow the Meeting auto choice.
Recording without transcription requires FFmpeg, not an ASR model.

Every saved session offers Transcribe with Qwen3, SenseVoice, or the configured
online ASR. This creates a new attempt, including for completed sessions. Retry
resumes the last failed attempt with its saved model/settings. Results retain
model, creation time and failure state. The last successful version remains
readable while another attempt runs or fails. Existing legacy transcripts remain
available. Voice cluster IDs are scoped to a result, never carried across runs.

## Desk mode

Desk mode is an explicit, local microphone listening mode inside Mettings.
It does not start at login or silently restart after an app restart. The window
and menu bar show active listening, with a direct Pause action. Pause closes the
microphone and flushes detected speech before returning.

A native Silero detector receives mono 16 kHz PCM continuously. It keeps a short
rolling buffer, about two seconds before speech and one second after it. Long
idle ambience is not archived. Completed clips contain natural brief pauses and
are bounded around 30 seconds. No speech produces no conversation/audio archive.
VAD is speech detection, not guaranteed noise removal or conversation relevance:
quiet speech may be missed; nearby voices or a playing video may be retained.

One Desk Start keeps all detected speech in one timestamped folder until
Pause/Stop. Quiet intervals do not close that session or create another folder.
The folder contains immutable clips, a playable concatenated recording, and a
clip manifest mapping retained audio times to original wall-clock times. Saving occurs before enqueueing ASR. An
interrupted app can lose the current unfinished in-memory clip, not older saved
clips. A capture failure visibly stops listening instead of claiming it is active.

Desk auto-ASR is separately remembered and off by default. When enabled it uses
the selected local ASR. A configured title service receives a transcript excerpt
when text first becomes available and again when the Desk run ends. Without
a title service, naming stays local.
Each newly sealed clip is queued once and appends to an automatic transcript;
older speech is not retranscribed on every update. Recording continues while
this serial queue works. Later explicit Transcribe with can use the complete
retained conversation and a different local or configured online engine.

## Data and integration boundaries

Capture and queue states are separate. `busy` covers configuration/install/import
operations, not ASR. A short data lock coordinates live session metadata/audio
snapshots; model inference never holds it. Audio and versions remain in session
folders. Failed attempts are retained and retryable. Old folders and their
transcripts are not migrated destructively.

The native runtime, microphone path, local models and Keychain boundaries stay
unchanged. Desk mode adds only a small speech detector model through the existing
verified installer, with no Python, new service, account or API key.

## Acceptance

- Finish with auto off preserves folder/audio and makes no ASR request.
- A completed session can be transcribed with another model without erasing the
  old result; failure leaves the prior result readable; retry uses the old model.
- Speech/silence/speech fixtures preserve speech beginnings/endings, exclude long
  idle spans, flush on stop and do not duplicate chunk boundaries.
- Long speech produces bounded clips; sustained quiet keeps the same session
  open. Later speech appends to its folder with correct wall-clock mapping,
  including across midnight. A new Start after Pause creates a new session.
- Desk capture and saved clip count continue while ASR is slow, queued or failing.
- Automatic desk results append each clip once; manual reruns remain separate.
- The native GUI exposes mode, active listening, auto choice, model and versions
  at normal and minimum window sizes, with accessible controls.

## Speech without speaker separation and empty results

Native ASR still runs when the speaker separator returns no segments. Such text
has no invented speaker identity; the result records `diarization_fallback` and
the transcript displays an unassigned speaker. Digital silence is not sent through
this fallback decoder.

A valid recognition result with no text is an `empty` attempt or clip, not a
service failure. Its input remains saved. Empty clips do not fail an otherwise
valid conversation, are skipped when retrying failed clips, and do not overwrite
an earlier successful transcript. A new attempt with another model remains
available. Malformed responses, model failures and transport failures remain
retryable errors. Historical failed attempts are not silently reclassified;
retry them to use the corrected processing path.
