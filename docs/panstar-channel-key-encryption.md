# Panstar channel credential encryption

Panstar production stores provider channel credentials as AES-256-GCM ciphertext. The
master key is independent from session, JWT, database, and external-billing secrets.

## Master key file

New API reads `/run/secrets/panstar-new-api/channel-key-master`. The file must be a
regular `0400` file readable only by the New API runtime user. Production sets:

```text
PANSTAR_CHANNEL_KEY_ENCRYPTION_REQUIRED=true
PANSTAR_CHANNEL_KEY_MASTER_FILE=/run/secrets/panstar-new-api/channel-key-master
```

The file is JSON. Key values are base64-encoded 32-byte AES keys:

```json
{
  "active_version": 1,
  "keys": {
    "1": "base64-encoded-32-byte-key"
  }
}
```

Do not put this file in Git, an image layer, an environment variable, a database
backup, shell history, or release evidence. Both production nodes must mount the same
keyring because they share encrypted channel rows.

## Storage and read contract

Each write uses a fresh random GCM nonce and AAD bound to the row's random cipher ID
and master-key version. The database stores ciphertext, nonce, master-key version,
credential version, and a last-four mask. The legacy `channels.key` column is empty
for encrypted credentials. Channel caches contain the encrypted fields, not provider
plaintext. Decryption occurs only after a channel has been selected for one request.

`POST /api/channel/{id}/key` returns metadata only. Full credential recovery is not
available. A root administrator rotates a credential through
`POST /api/channel/{id}/key/rotate`; the request requires a single-use
`channel.key.write` security proof and a unique `Idempotency-Key`. Responses and audit
records contain only versions and the mask.

## Rotation and recovery

To rotate the master key, add a new numbered entry while retaining every version used
by existing rows, change `active_version`, deploy the same keyring to both nodes, and
restart serially. Rotate channel credentials separately through the protected admin
flow. Remove an old master version only after every row has been re-encrypted and a
backup-restore drill has decrypted all rows with the retained keyring.

A database backup alone cannot recover provider credentials. A recovery drill must
restore the database and the separately escrowed keyring into an isolated environment,
prove ciphertext-only database rows, perform one synthetic channel dispatch, and then
destroy the restored secret material.
