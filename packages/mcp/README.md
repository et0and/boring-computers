# @boring/mcp

An [MCP](https://modelcontextprotocol.io) server that lets any AI spin up and
drive a real Linux computer — a Firecracker microVM from
[boring computers](https://boringcomputers.com).

Tools: `launch_computer`, `run_task` (give it a plain-English task and an agent
writes + runs the code, returning a live preview URL if it starts a server),
`screenshot`, `preview_url`, `fork_computer`, `list_computers`, `stop_computer`.

No key required — it uses the public, rate-limited endpoint.

## Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
	"mcpServers": {
		"boring-computers": {
			"command": "npx",
			"args": ["-y", "@boring/mcp"]
		}
	}
}
```

Then ask Claude: _"launch a computer and build me a snake game."_

## Any MCP client / local run

```bash
npx -y @boring/mcp
# or point at a different endpoint:
BORING_URL=https://your-boringd node index.mjs
```
