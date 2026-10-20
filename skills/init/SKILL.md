---
name: init
description: This skill should be used when the user runs /auto-context:init (or $init in Codex) to set up auto-context in a project — find units, confirm them and the docs language with the user, and create the index files (CLAUDE.md, or AGENTS.md in Codex) and context/ docs for each.
disable-model-invocation: true
---
# /auto-context:init (Codex: $init)

0. In Codex, first of all — before creating any docs — set `index_file: AGENTS.md` in
   `.auto-context/config.yaml` (create the file and its directory if missing; keep existing content).
   The index file of a unit (`index_file`, `CLAUDE.md` by default) is what "index file" means below.
1. Run `auto-context units`. Each line is `<unit path>\t<reason>\t<file count>` (reason: `module`,
   `files`, `include` or `root`). Show the user a table: path, reason, number of files. Mention any
   `warning:` lines (config problems, skipped `.gitignore` files, a git failure). If it fails with
   `… cannot be read or parsed; fix it first`, `.auto-context/config.yaml` is broken: have the user
   fix it (or fix it with their approval), then start again.
2. Ask the user to confirm the list and which language to write the docs in. Do not create any docs
   before they answer.
   - Language: set `language: <code>` (e.g. `ru`, `en`, `pt-BR`) in `.auto-context/config.yaml`, keeping
     existing content. If the user has no preference, leave it unset.
   - Writing `.auto-context/config.yaml` (or `context/_root.md`) activates the hooks: from then on Stop may
     auto-ack docs written in a turn and report code edited meanwhile. That is expected; steps 3–4
     handle those units.
   - Each rejected unit: `auto-context ack --exclude <unit>` (the user approves the command). `_root`
     cannot be excluded — it is always documented. To ignore a directory entirely, follow the exclude
     rule of the docs skill.
   - Each extra directory to document: add it to `include` in `.auto-context/config.yaml` (create the file and its directory
     if missing; keep existing content), then rerun `auto-context units`:
     ```yaml
     include:
       - path/to/dir
     ```
3. Invoke the `auto-context:auto-context-docs` skill (Codex: `$auto-context-docs`), then run `auto-context status --stale` and
   document each listed unit from its code, as that skill says:
   - `missing` → create `context/<unit>.md` and `<unit>/<index file>` (edit an existing index file in
     place, as the docs skill says — the root one usually holds the user's own content);
   - `stale` → a context doc exists (an earlier run, or written by hand): update it, do not rewrite;
   - `stale (…)` with a note → as "Status notes" of the docs skill says;
   - `note: … belongs to no unit` → docs of a removed unit: apply the "Unit removed" rule of the docs
     skill (ask the user first).
   For many units, work in batches and keep each batch's context small.
4. Run `auto-context ack <unit…>` for the documented units — this also writes the
   `auto-context:units` index into the root index file — then `auto-context status --stale` and
   confirm it says `all docs up to date`.
