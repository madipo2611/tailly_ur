path "kv/data/digital-notary/api" { capabilities = ["read"] }
path "pki_int/issue/digital-notary-client" { capabilities = ["update"] }
# Write a dedicated policy for a signing adapter. It must receive a short-lived
# mTLS client certificate, never the private key of a customer's UKEP.
