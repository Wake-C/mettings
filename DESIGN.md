# Mettings design

## Theme
A quiet meeting notebook. Recording and returning to the transcript lead;
provider configuration stays in a separate Settings window.

## Palette
Reuse InlineExplain: paper #ffffff, ink #27382f, muted #647268,
accent #367359, context #f2f5f3, line #dde3d9, error #9f3830.
The session sidebar uses #eef2ee to separate navigation from reading.

## Typography
Georgia with Songti SC for meeting headings. System and PingFang SC for
controls and transcripts. Transcript text uses 1.9 line height; timestamps use
tabular numbers. No remote fonts.

## Components
Plain session rows, a single primary recording control, restrained text actions,
and labeled form fields. Radius scale: 7, 11, 18 px. No dashboard card grid.

## Layout
Session list at left; the in-app meeting folder browser or transcript at right.
Folders use breadcrumbs and simple file rows. Text previews and audio playback
stay in the window. Finder is not the normal navigation path. Settings is a separate scrollable
window with a persistent save action. Empty state explains the next action.

## Depth
Flat paper and background steps. No decorative shadows or gradients.

## Do and do not
Keep errors next to the session. Preserve audio when processing fails. Do not
invent speaker labels when a provider returns text only. Never show a key again.

## Responsive behavior
Main window minimum 720 × 520; normal 1060 × 740. Settings minimum 500 × 520.
Both reading and session navigation scroll independently. Actions wrap.

## Interaction and implementation
Plain CSS is the single styling strategy, matching InlineExplain's frontend.
No entrance animation. Pointer presses scale to .96, except reduced-motion mode.
Visible keyboard focus; no animation on keyboard-driven navigation.

## Application identity
The app icon follows InlineExplain's black-on-white tile treatment and Peekturn's
recognizable silhouette principle. Two overlapping voice bubbles and three
waveform cutouts identify meetings. The menu-bar version uses the same silhouette
as a transparent template glyph, independently drawn for 18 pt rather than
shrinking the full app tile. No idle menu-bar wordmark; a recording dot is added
only while recording.

## Capture and transcription controls
Meeting and Desk are a compact switch on the capture surface. Finish means save;
a separate, remembered per-mode checkbox requests automatic transcription. Desk
shows a persistent listening dot, retained speech duration first and listening
time second. Pause remains available while ASR is busy. Model choice and version
history belong with the selected recording, not inside occasional global settings.
Do not hide previous successful text behind a failed attempt. Folder browsing
and saved-audio playback remain usable without installed ASR models.

## Settings hierarchy
Basic owns microphone, save location, the local model and per-mode auto-ASR
choices. Advanced owns optional online providers, title services, vocabulary,
voice matching and component details. Basic opens by default; Advanced groups
remain collapsed until needed. Keep the tabs and Save action visible while the
content scrolls. Preserve saved preferences and merge only edited fields.

Use concise product labels and operational facts. Do not put conversation-derived
slogans, design explanations or model superiority claims in the interface. The
model picker uses the native catalog for names, tiers and download sizes.


## Speaker review
Show one row per acoustic group for unconfirmed voices, with its segment count
and a representative listening action. Show three pending groups initially and
reveal more on request. Recognized identities are grouped by saved name in a
collapsed section. A group is a matching suggestion, not a verified person.
Keep individual segment playback and correction available inside each group;
load those controls only when opened. Preserve drafts and disclosure state while
Desk updates arrive. Raw matching scores belong in the expanded review, so the
speaker list does not bury the transcript.
