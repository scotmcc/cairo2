package server

// DefaultPort is the TCP port cairo serve listens on when --port is not set.
const DefaultPort = 1337

// TokenBytes is the number of random bytes used to generate a bearer token.
// 8 bytes encodes to 16 hex characters.
const TokenBytes = 8

// ModelID is the model identifier returned in OpenAI-compatible responses.
const ModelID = "cairo"

// BridgeQueueDepth is the buffered channel depth for the session bridge
// request queue. Requests beyond this depth block until the worker drains.
const BridgeQueueDepth = 32
