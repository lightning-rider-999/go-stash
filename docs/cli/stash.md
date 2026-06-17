## stash

Agent-first CLI for the Stash GraphQL API

### Synopsis

stash is a machine-readable command-line client for a self-hosted Stash instance. Every Stash GraphQL operation is exposed as a resource-and-verb command (e.g. `stash scene list`, `stash metadata scan`). Output is JSON by default.

```
stash [flags]
```

### Options

```
      --api-key string   Stash API key (default $STASHAPP_API_KEY)
  -h, --help             help for stash
      --input string     variables source: JSON file path, or "-" for stdin
  -o, --output string    output format: json, ndjson, table, yaml (default "json")
      --url string       Stash base URL (default $STASHAPP_URL)
```

### SEE ALSO

* [stash catalog](stash_catalog.md)	 - Print the embedded machine-facing operation catalog
* [stash config](stash_config.md)	 - config operations
* [stash file](stash_file.md)	 - file operations
* [stash folder](stash_folder.md)	 - folder operations
* [stash gallery](stash_gallery.md)	 - gallery operations
* [stash gallery-chapter](stash_gallery-chapter.md)	 - gallery-chapter operations
* [stash group](stash_group.md)	 - group operations
* [stash image](stash_image.md)	 - image operations
* [stash job](stash_job.md)	 - job operations
* [stash log](stash_log.md)	 - log operations
* [stash metadata](stash_metadata.md)	 - metadata operations
* [stash misc](stash_misc.md)	 - misc operations
* [stash movie](stash_movie.md)	 - movie operations
* [stash performer](stash_performer.md)	 - performer operations
* [stash saved-filter](stash_saved-filter.md)	 - saved-filter operations
* [stash scan](stash_scan.md)	 - scan operations
* [stash scene](stash_scene.md)	 - scene operations
* [stash scene-marker](stash_scene-marker.md)	 - scene-marker operations
* [stash scrape](stash_scrape.md)	 - scrape operations
* [stash stash-box](stash_stash-box.md)	 - stash-box operations
* [stash studio](stash_studio.md)	 - studio operations
* [stash tag](stash_tag.md)	 - tag operations

