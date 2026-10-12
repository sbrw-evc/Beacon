path "umbrella/data/*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "umbrella/metadata/*" {
  capabilities = ["read", "delete", "list"]
}

path "auth/token/lookup-self" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
