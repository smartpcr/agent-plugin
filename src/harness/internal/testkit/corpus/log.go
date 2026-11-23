package corpus

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Records per turn: turn/start, user/message, the final step (step/start,
// assistant/message, step/end) and turn/end, plus five per tool step
// (step/start, assistant/message with the tool use, tool/call, tool/result,
// step/end).
const (
	turnRecords = 6
	stepRecords = 5
)

// Log is a valid log of one session of shape sh: sh.Turns completed turns, each
// a prompt, tool steps and a final answer, at least sh.Events records whose
// payloads total at least sh.Bytes (the tool results carry the padding). The
// tool steps are spread evenly over the turns. Record i is stamped start+i
// (Unix milliseconds), so the session spans one millisecond per record. The same
// shape and start always give the same log.
func Log(sh Shape, start int64) ([]event.Record, error) {
	if sh.Turns <= 0 || sh.Events < 0 || sh.Bytes < 0 {
		return nil, fmt.Errorf("corpus: a session of %d turns, %d events and %d bytes", sh.Turns, sh.Events, sh.Bytes)
	}
	steps := max(0, (sh.Events-turnRecords*sh.Turns+stepRecords-1)/stepRecords)
	// The first pass, unpadded, measures what the padding must add.
	recs, err := build(sh.Turns, steps, 0, start)
	if err != nil {
		return nil, err
	}
	if short := sh.Bytes - Bytes(recs); short > 0 && steps > 0 {
		recs, err = build(sh.Turns, steps, (short+steps-1)/steps, start)
	}
	return recs, err
}

// Bytes is the payload bytes of recs, hot and cold parts.
func Bytes(recs []event.Record) int {
	n := 0
	for _, r := range recs {
		n += len(r.Payload) + len(r.Cold)
	}
	return n
}

// build is turns turns holding steps tool steps between them, each tool result
// padded by pad bytes.
func build(turns, steps, pad int, start int64) ([]event.Record, error) {
	recs := make([]event.Record, 0, turnRecords*turns+stepRecords*steps)
	filler := padding(pad)
	for t := 1; t <= turns; t++ {
		k := steps / turns
		if t <= steps%turns {
			k++
		}
		for _, d := range turnDrafts(t, k, filler) {
			if d.err != nil {
				return nil, d.err
			}
			seq := event.Seq(len(recs))
			rec, err := d.d.Stamp(seq, start+int64(seq))
			if err != nil {
				return nil, err
			}
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

type draft struct {
	d   event.Draft
	err error
}

func of(d event.Draft, err error) draft { return draft{d, err} }

func msg(id string, role vocab.Role, b vocab.Block) vocab.Message {
	m, _ := vocab.RestoreMessage(id, role, nil, b)
	return m
}

// turnDrafts is turn t with k tool steps, each tool result's text filler.
func turnDrafts(t, k int, filler string) []draft {
	ds := []draft{
		of(event.TurnStartType.Draft(event.TurnStart{Turn: t})),
		of(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
			Message: msg(fmt.Sprintf("u%d", t), vocab.RoleUser, vocab.TextBlock(fmt.Sprintf("prompt %d: read the next files and explain them", t)))})),
	}
	for s := 1; s <= k; s++ {
		call := fmt.Sprintf("c%d-%d", t, s)
		args := fmt.Sprintf(`{"file":"f%d.go"}`, s)
		ds = append(ds,
			of(event.StepStartType.Draft(event.StepStart{Turn: t, Step: s})),
			of(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
				Message: msg(fmt.Sprintf("a%d-%d", t, s), vocab.RoleAssistant, vocab.ToolUseBlock(call, "read", json.RawMessage(args))),
				Usage:   &vocab.Usage{Input: 1000 + s, Output: 20, Total: 1020 + s}})),
			of(event.ToolCall.Draft(event.ToolCallPayload{CallID: call, Name: "read", Arguments: args})),
			of(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: call,
				Message: msg(fmt.Sprintf("r%d-%d", t, s), vocab.RoleTool, vocab.ToolResultBlock(call, false, vocab.TextPart(filler)))})),
			of(event.StepEndType.Draft(event.StepEnd{Turn: t, Step: s})),
		)
	}
	return append(ds,
		of(event.StepStartType.Draft(event.StepStart{Turn: t, Step: k + 1})),
		of(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: msg(fmt.Sprintf("a%d-%d", t, k+1), vocab.RoleAssistant, vocab.TextBlock(fmt.Sprintf("answer %d", t))),
			Usage:   &vocab.Usage{Input: 1200, Output: 80, Total: 1280}})),
		of(event.StepEndType.Draft(event.StepEnd{Turn: t, Step: k + 1})),
		of(event.TurnEndType.Draft(event.TurnEnd{Turn: t, Reason: event.Reason{Kind: event.ReasonCompleted}})),
	)
}

// padding is a tool result's text: "ok" and n more bytes of plain text that
// JSON encodes as itself.
func padding(n int) string {
	const line = "package corpus; a line of a file the tool read. "
	return "ok" + strings.Repeat(line, max(n, 0)/len(line)+1)[:max(n, 0)]
}
