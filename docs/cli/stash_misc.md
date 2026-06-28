## stash misc

misc operations

```
stash misc [flags]
```

### Options

```
  -h, --help   help for misc
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
* [stash misc add-gallery-images](stash_misc_add-gallery-images.md)	 - AddGalleryImages (mutation)
* [stash misc add-group-sub-groups](stash_misc_add-group-sub-groups.md)	 - AddGroupSubGroups (mutation)
* [stash misc add-temp-dlnaip](stash_misc_add-temp-dlnaip.md)	 - AddTempDLNAIP (mutation)
* [stash misc anonymise-database](stash_misc_anonymise-database.md)	 - AnonymiseDatabase (mutation) [destructive]
* [stash misc available-packages](stash_misc_available-packages.md)	 - AvailablePackages (query)
* [stash misc backup-database](stash_misc_backup-database.md)	 - BackupDatabase (mutation)
* [stash misc delete-files](stash_misc_delete-files.md)	 - DeleteFiles (mutation) [destructive]
* [stash misc destroy-files](stash_misc_destroy-files.md)	 - DestroyFiles (mutation) [destructive]
* [stash misc destroy-saved-filter](stash_misc_destroy-saved-filter.md)	 - DestroySavedFilter (mutation) [destructive]
* [stash misc directory](stash_misc_directory.md)	 - Directory (query)
* [stash misc disable-dlna](stash_misc_disable-dlna.md)	 - DisableDLNA (mutation)
* [stash misc dlna-status](stash_misc_dlna-status.md)	 - DlnaStatus (query)
* [stash misc download-ff-mpeg](stash_misc_download-ff-mpeg.md)	 - DownloadFFMpeg (mutation) [async job]
* [stash misc enable-dlna](stash_misc_enable-dlna.md)	 - EnableDLNA (mutation)
* [stash misc exec-sql](stash_misc_exec-sql.md)	 - ExecSQL (mutation) [destructive]
* [stash misc export-objects](stash_misc_export-objects.md)	 - ExportObjects (mutation)
* [stash misc file-set-fingerprints](stash_misc_file-set-fingerprints.md)	 - FileSetFingerprints (mutation)
* [stash misc generate-api-key](stash_misc_generate-api-key.md)	 - GenerateAPIKey (mutation)
* [stash misc image-decrement-o](stash_misc_image-decrement-o.md)	 - ImageDecrementO (mutation)
* [stash misc image-increment-o](stash_misc_image-increment-o.md)	 - ImageIncrementO (mutation)
* [stash misc image-reset-o](stash_misc_image-reset-o.md)	 - ImageResetO (mutation)
* [stash misc import-objects](stash_misc_import-objects.md)	 - ImportObjects (mutation) [async job]
* [stash misc install-packages](stash_misc_install-packages.md)	 - InstallPackages (mutation) [async job]
* [stash misc installed-packages](stash_misc_installed-packages.md)	 - InstalledPackages (query)
* [stash misc job-queue](stash_misc_job-queue.md)	 - JobQueue (query)
* [stash misc latestversion](stash_misc_latestversion.md)	 - Latestversion (query)
* [stash misc list-scrapers](stash_misc_list-scrapers.md)	 - ListScrapers (query)
* [stash misc logs](stash_misc_logs.md)	 - Logs (query)
* [stash misc marker-strings](stash_misc_marker-strings.md)	 - MarkerStrings (query)
* [stash misc marker-wall](stash_misc_marker-wall.md)	 - MarkerWall (query)
* [stash misc migrate](stash_misc_migrate.md)	 - Migrate (mutation) [destructive, async job]
* [stash misc migrate-blobs](stash_misc_migrate-blobs.md)	 - MigrateBlobs (mutation) [destructive, async job]
* [stash misc migrate-hash-naming](stash_misc_migrate-hash-naming.md)	 - MigrateHashNaming (mutation) [async job]
* [stash misc migrate-scene-screenshots](stash_misc_migrate-scene-screenshots.md)	 - MigrateSceneScreenshots (mutation) [async job]
* [stash misc move-files](stash_misc_move-files.md)	 - MoveFiles (mutation)
* [stash misc optimise-database](stash_misc_optimise-database.md)	 - OptimiseDatabase (mutation) [async job]
* [stash misc parse-scene-filenames](stash_misc_parse-scene-filenames.md)	 - ParseSceneFilenames (query)
* [stash misc plugin-tasks](stash_misc_plugin-tasks.md)	 - PluginTasks (query)
* [stash misc plugins](stash_misc_plugins.md)	 - Plugins (query)
* [stash misc query-sql](stash_misc_query-sql.md)	 - QuerySQL (mutation) [destructive]
* [stash misc reload-plugins](stash_misc_reload-plugins.md)	 - ReloadPlugins (mutation)
* [stash misc reload-scrapers](stash_misc_reload-scrapers.md)	 - ReloadScrapers (mutation)
* [stash misc remove-gallery-images](stash_misc_remove-gallery-images.md)	 - RemoveGalleryImages (mutation)
* [stash misc remove-group-sub-groups](stash_misc_remove-group-sub-groups.md)	 - RemoveGroupSubGroups (mutation)
* [stash misc remove-temp-dlnaip](stash_misc_remove-temp-dlnaip.md)	 - RemoveTempDLNAIP (mutation)
* [stash misc reorder-sub-groups](stash_misc_reorder-sub-groups.md)	 - ReorderSubGroups (mutation)
* [stash misc reset-gallery-cover](stash_misc_reset-gallery-cover.md)	 - ResetGalleryCover (mutation)
* [stash misc reveal-file-in-file-manager](stash_misc_reveal-file-in-file-manager.md)	 - RevealFileInFileManager (mutation)
* [stash misc reveal-folder-in-file-manager](stash_misc_reveal-folder-in-file-manager.md)	 - RevealFolderInFileManager (mutation)
* [stash misc run-plugin-operation](stash_misc_run-plugin-operation.md)	 - RunPluginOperation (mutation)
* [stash misc run-plugin-task](stash_misc_run-plugin-task.md)	 - RunPluginTask (mutation)
* [stash misc save-filter](stash_misc_save-filter.md)	 - SaveFilter (mutation)
* [stash misc scene-add-o](stash_misc_scene-add-o.md)	 - SceneAddO (mutation)
* [stash misc scene-add-play](stash_misc_scene-add-play.md)	 - SceneAddPlay (mutation)
* [stash misc scene-assign-file](stash_misc_scene-assign-file.md)	 - SceneAssignFile (mutation)
* [stash misc scene-delete-o](stash_misc_scene-delete-o.md)	 - SceneDeleteO (mutation)
* [stash misc scene-delete-play](stash_misc_scene-delete-play.md)	 - SceneDeletePlay (mutation)
* [stash misc scene-generate-screenshot](stash_misc_scene-generate-screenshot.md)	 - SceneGenerateScreenshot (mutation)
* [stash misc scene-marker-tags](stash_misc_scene-marker-tags.md)	 - SceneMarkerTags (query)
* [stash misc scene-reset-activity](stash_misc_scene-reset-activity.md)	 - SceneResetActivity (mutation)
* [stash misc scene-reset-o](stash_misc_scene-reset-o.md)	 - SceneResetO (mutation)
* [stash misc scene-reset-play-count](stash_misc_scene-reset-play-count.md)	 - SceneResetPlayCount (mutation)
* [stash misc scene-save-activity](stash_misc_scene-save-activity.md)	 - SceneSaveActivity (mutation)
* [stash misc scene-streams](stash_misc_scene-streams.md)	 - SceneStreams (query)
* [stash misc scene-wall](stash_misc_scene-wall.md)	 - SceneWall (query)
* [stash misc set-gallery-cover](stash_misc_set-gallery-cover.md)	 - SetGalleryCover (mutation)
* [stash misc set-plugins-enabled](stash_misc_set-plugins-enabled.md)	 - SetPluginsEnabled (mutation)
* [stash misc setup](stash_misc_setup.md)	 - Setup (mutation)
* [stash misc stats](stash_misc_stats.md)	 - Stats (query)
* [stash misc stop-all-jobs](stash_misc_stop-all-jobs.md)	 - StopAllJobs (mutation)
* [stash misc stop-job](stash_misc_stop-job.md)	 - StopJob (mutation)
* [stash misc system-status](stash_misc_system-status.md)	 - SystemStatus (query)
* [stash misc uninstall-packages](stash_misc_uninstall-packages.md)	 - UninstallPackages (mutation) [async job]
* [stash misc update-packages](stash_misc_update-packages.md)	 - UpdatePackages (mutation) [async job]
* [stash misc validate-stash-box-credentials](stash_misc_validate-stash-box-credentials.md)	 - ValidateStashBoxCredentials (query)
* [stash misc version](stash_misc_version.md)	 - Version (query)

