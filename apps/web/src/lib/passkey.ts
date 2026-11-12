// In 0.3 a critical request asks for Face ID itself (lib/api.ts request():
// the brain answers "reauth", the page signs an intent and retries). The
// components keep their retry buttons; retrying is all this needs to do.
export async function reauthenticate() {}
