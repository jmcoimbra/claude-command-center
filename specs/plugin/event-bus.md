# SPEC: Event Bus

## Purpose

Provide a typed pub/sub event bus for inter-plugin communication. Plugins publish events and subscribe to topics without direct references to each other.

## Interface

```go
type Event struct {
    Source  string
    Topic   string
    Payload any
}

type EventBus interface {
    Publish(event Event)
    Subscribe(topic string, handler func(Event))
}
```

## Behavior

- Subscribe registers a handler for a topic. Multiple handlers per topic allowed.
- Publish delivers the event to all handlers subscribed to the event's Topic.
- Events are delivered synchronously in the order handlers were registered.
- Publishing to a topic with no subscribers is a no-op (no error).
- Handlers should not block (long operations should return tea.Cmd via Action).
- **Concurrency rule:** Event bus handlers MUST NOT mutate shared plugin state that is also accessed by tea.Cmd goroutines or View(). For daemon events (`data.refreshed`, `session.*`), use `plugin.NotifyMsg` in `HandleMessage` to dispatch async `Refresh()` cmds instead — this ensures all state mutations happen on the main bubbletea loop.

## Event Catalog

### Command Center Events

| Topic | Payload | Subscribers | Description |
|-------|---------|-------------|-------------|
| `todo.completed` | `{id, title}` | settings (log) | Todo marked done |
| `todo.created` | `{id, title, source}` | settings (log) | New todo created |
| `todo.dismissed` | `{id, title}` | settings (log) | Todo dismissed |
| `todo.deferred` | `{id, title}` | settings (log) | Todo deferred |
| `todo.promoted` | `{id, title}` | settings (log) | Todo promoted to top |
| `todo.edited` | `{id, title}` | settings (log) | Todo edited via LLM |
| `pending.todo` | `{todo_id, title, context, detail, who_waiting, due, effort}` | sessions | User selected a todo for launch without a project dir |
| `data.refreshed` | `{source}` | — (via NotifyMsg) | ai-cron completed |

**Note:** Daemon events (`session.registered`, `session.updated`, `session.ended`) are also routed through the event bus via `routeDaemonEvent`, and broadcast as `plugin.NotifyMsg` to all plugins. They are documented in the daemon spec.

### Sessions Events

| Topic | Payload | Subscribers | Description |
|-------|---------|-------------|-------------|
| `pending.todo.cancel` | `{}` | commandcenter | User cancelled pending todo selection |
| `session.bookmark.created` | `{session_id, project}` | — | Session bookmarked |
| `session.bookmark.deleted` | `{session_id}` | — | Session bookmark removed |

### Knowledge Events

| Topic | Payload | Subscribers | Description |
|-------|---------|-------------|-------------|
| `knowledge.insights.updated` | none | commandcenter | Knowledge plugin wrote or removed insights in `knowledge_surfaced_insights`; consumers re-query the table |

### Settings Events

| Topic | Payload | Subscribers | Description |
|-------|---------|-------------|-------------|
| `config.saved` | `{keys_changed}` | commandcenter | Config file saved |
| `palette.changed` | `{previous, new}` | — | Color palette changed |
| `datasource.toggled` | `{name, enabled}` | — | Data source toggled on/off |

## Starter Interface

Plugins that need to run initial tea.Cmds (e.g., spinner ticks) implement:

```go
type Starter interface {
    StartCmds() tea.Cmd
}
```

The host iterates all plugins in `Init()`, checks for `Starter`, and collects cmds.
This replaces direct plugin-specific init calls in the host.

## Test Cases

- Subscribe then Publish delivers to handler
- Multiple subscribers all receive the event
- Publish with no subscribers does not panic
- Handler receives correct Event fields
- Multiple topics can be subscribed independently
