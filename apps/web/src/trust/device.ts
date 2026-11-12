// The device key (decision 0033): an AES-GCM key generated in this browser,
// non-extractable, kept in IndexedDB. It seals the identity for a passkey
// that has no PRF; the relay holds that ciphertext and releases it only for
// a fresh assertion of the same passkey.

const DB = 'sous-device'
const STORE = 'keys'

function db(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB, 1)
    req.onupgradeneeded = () => req.result.createObjectStore(STORE)
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error ?? new Error('浏览器不让保存设备密钥'))
  })
}

async function tx<T>(mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const d = await db()
  try {
    return await new Promise<T>((resolve, reject) => {
      const req = fn(d.transaction(STORE, mode).objectStore(STORE))
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error)
    })
  } finally {
    d.close()
  }
}

export async function newDeviceKey(credId: string): Promise<CryptoKey> {
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
  await tx('readwrite', (s) => s.put(key, credId))
  return key
}

export async function loadDeviceKey(credId: string): Promise<CryptoKey | null> {
  try {
    return ((await tx('readonly', (s) => s.get(credId))) as CryptoKey | undefined) ?? null
  } catch {
    return null
  }
}

export async function deleteDeviceKey(credId: string): Promise<void> {
  try {
    await tx('readwrite', (s) => s.delete(credId))
  } catch {
    // nothing to forget
  }
}
