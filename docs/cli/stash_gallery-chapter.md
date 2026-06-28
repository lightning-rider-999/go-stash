## stash gallery-chapter

gallery-chapter operations

```
stash gallery-chapter [flags]
```

### Options

```
  -h, --help   help for gallery-chapter
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
* [stash gallery-chapter create](stash_gallery-chapter_create.md)	 - GalleryChapterCreate (mutation)
* [stash gallery-chapter destroy](stash_gallery-chapter_destroy.md)	 - GalleryChapterDestroy (mutation) [destructive]
* [stash gallery-chapter update](stash_gallery-chapter_update.md)	 - GalleryChapterUpdate (mutation)

