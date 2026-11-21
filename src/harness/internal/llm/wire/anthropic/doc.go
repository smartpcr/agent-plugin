// Package anthropic is the Anthropic Messages wire driver (DH-D-LLM-2): Encode
// renders a request surface as a streaming POST /v1/messages body and Decode turns
// the event stream of the response into the runtime's chunks.
//
// The driver is vendor-neutral within the dialect: a route (deepseek-official)
// supplies the endpoint, the authentication headers and the vendor error table.
//
//dsh:untrusted-parser Messages event streams and error bodies arrive from the network
package anthropic
