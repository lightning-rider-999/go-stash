## stash stash-box

stash-box operations

```
stash stash-box [flags]
```

### Options

```
  -h, --help   help for stash-box
```

### Options inherited from parent commands

```
      --allow-partial    on an HTTP-200 response that also carries GraphQL errors, still print the partial data to stdout; the error envelope and non-zero exit are unchanged
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash](stash.md)	 - Agent-first CLI for the Stash GraphQL API
* [stash stash-box batch-performer-tag](stash_stash-box_batch-performer-tag.md)	 - StashBoxBatchPerformerTag (mutation) [async job]
* [stash stash-box batch-studio-tag](stash_stash-box_batch-studio-tag.md)	 - StashBoxBatchStudioTag (mutation) [async job]
* [stash stash-box batch-tag-tag](stash_stash-box_batch-tag-tag.md)	 - StashBoxBatchTagTag (mutation) [async job]
* [stash stash-box submit-fingerprints](stash_stash-box_submit-fingerprints.md)	 - SubmitStashBoxFingerprints (mutation)
* [stash stash-box submit-performer-draft](stash_stash-box_submit-performer-draft.md)	 - SubmitStashBoxPerformerDraft (mutation)
* [stash stash-box submit-scene-draft](stash_stash-box_submit-scene-draft.md)	 - SubmitStashBoxSceneDraft (mutation)

