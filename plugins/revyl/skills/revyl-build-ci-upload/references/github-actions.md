# GitHub Actions upload step

Add these steps to the job that builds the app, after the build step. The
runner installs its own Revyl CLI.

```yaml
      - name: Install Revyl CLI
        run: |
          curl -fsSL https://revyl.com/install.sh | sh
          echo "$HOME/.revyl/bin" >> "$GITHUB_PATH"

      - name: Upload build to Revyl
        env:
          REVYL_API_KEY: ${{ secrets.REVYL_API_KEY }}
        run: |
          revyl build upload \
            --file path/to/MyApp.app.zip \
            --platform ios \
            --app <ios-app-uuid> \
            --yes \
            --json
```

- On `pull_request` events, `actions/checkout` checks out a merge commit by
  default, while the upload records the pull request's head commit. Build from
  the head commit so the two agree:

  ```yaml
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha || github.sha }}
  ```

- Pull requests from forks don't receive repository secrets, so the upload
  step can't authenticate there. Guard it with
  `if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository`,
  which skips only fork pull requests and keeps uploads on pushes and other
  events.
- `--file` takes the simulator `.app` bundle itself or an archive of it
  (`ditto -c -k --keepParent MyApp.app MyApp.app.zip` on macOS runners).
