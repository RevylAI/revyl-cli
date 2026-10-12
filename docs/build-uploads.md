# Upload existing builds

Upload a local iOS artifact or let Revyl fetch it from your CI provider:

```bash
revyl build upload --file ./Clip.app.zip --platform ios --app <app-id>
revyl build upload --url "https://example.com/build.gz" --platform ios --app <app-id>
```

Add `--branch staging` to select a Revyl destination branch; otherwise the upload
uses the app default. A named destination can create the branch. Version
labels do not select the destination.
The destination stays fixed
through upload finalization. An upload pins its branch to the new build, even
when the branch was following Latest; add `--no-set-current` to leave the
branch's pin unchanged.
Results include `branch_id`, `branch_name`, and the exact uploaded build ID.
See [Build branches](build-branches.md) for selection and listing commands.
After a successful command exit, read the JSON for the upload mode:

- `build upload --file ... --json`: require `.success == true` and `.count == 1`,
  then use `.build.build_id`.
- `build upload --url ... --json`: require a non-empty `.version_id` and use that
  exact build ID. URL uploads return a flat object without `success`, `count`, or
  `build` fields.

Pass that exact ID to downstream Explore or test runs. Uploading alone does not
populate Atlas; see [maintaining main or staging](explore.md#keep-main-or-staging-current).

For staged local uploads, the CLI allows up to 90 seconds per request
attempt for the server to validate and finalize the build. Ordinary API calls
retain their 30-second timeout. Existing retry limits and cancellation still
apply; a timeout does not prove that the server rejected the build.

Simulator-built standalone App Clips are supported. Upload the Clip's `.app`
bundle or an archive containing it; its `NSAppClip` metadata is preserved and
the build is registered with the Clip's bundle identifier. An archive containing
a host application and its embedded Clip selects the host. Upload the Clip
separately to test it independently.

Both `--file` and `--url` accept iOS gzip-compressed tar archives named `.gz`,
`.tar.gz`, or `.tgz`. ZIP-content EAS artifacts using these suffixes also work.
The CLI converts local archives before uploading; Revyl processes URL artifacts
on the server. Arbitrary gzip-compressed files without an app bundle are invalid.

Local conversion allows up to 8 GiB of temporary files, 8 GiB of uncompressed
data during repacking, and a 5 GiB output artifact. Limits apply per process;
a small compressed archive can still exceed them.

Archive entries with duplicate paths, including case or Unicode equivalents,
are rejected. Internal symbolic links are preserved independently of entry
order, without requiring Windows symlink privileges or Developer Mode for local
archive conversion. Extraction files are removed after conversion; if that cleanup
fails, the CLI warns with the temporary directory to remove and keeps the completed ZIP.

Build App Clips for the same simulator destination as ordinary simulator apps.
App Clip support does not change the distinction between simulator builds and
physical-device IPAs. See the [artifact requirements](https://docs.revyl.com/builds/artifact-requirements).
