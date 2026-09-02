// Stub: the upstream (PasarGuard) hook detects text direction via i18next,
// which this fork doesn't use (Russian-only UI, no RTL locales). Kept as a
// hook (not inlined) so components ported from there needed no other edits.
export default function useDirDetection(): 'ltr' | 'rtl' {
  return 'ltr'
}
