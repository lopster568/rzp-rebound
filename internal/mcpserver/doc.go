// Package mcpserver exposes the risk-item tools over MCP so an agent can work
// one merchant's revenue at risk.
//
// The agent's whole reach is the eight tools in ToolNames, and every one of
// them passes through two layers: receiving middleware that knows only a tool
// name and a risk item id, and a policy evaluation that is the first statement
// of the shared action path. Neither layer knows what a tool does, which is why
// the surface could be replaced without either being rewritten.
package mcpserver
