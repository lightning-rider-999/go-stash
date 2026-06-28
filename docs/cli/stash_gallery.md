## stash gallery

gallery operations

```
stash gallery [flags]
```

### Options

```
  -h, --help   help for gallery
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
* [stash gallery bulk-update](stash_gallery_bulk-update.md)	 - BulkGalleryUpdate (mutation)
* [stash gallery create](stash_gallery_create.md)	 - GalleryCreate (mutation)
* [stash gallery destroy](stash_gallery_destroy.md)	 - GalleryDestroy (mutation) [destructive]
* [stash gallery get](stash_gallery_get.md)	 - FindGallery (query)
* [stash gallery list](stash_gallery_list.md)	 - FindGalleries (query)
* [stash gallery update](stash_gallery_update.md)	 - GalleryUpdate (mutation)
* [stash gallery update-many](stash_gallery_update-many.md)	 - GalleriesUpdate (mutation)

