# Homebrew tap packaging

The public tap is [`VladCuciureanu/homebrew-tap`](https://github.com/VladCuciureanu/homebrew-tap); it holds no formulas until the first release. Betty keeps reviewed
formula templates here so a release can update that repository without putting
vendor drivers in the base formula.

After publishing an immutable `vX.Y.Z` tag, download Betty's release source
archive (`betty-X.Y.Z.tar.gz`), calculate its SHA-256, and render the formulas
into the tap's `Formula` directory:

```console
./packaging/homebrew/render-formulas.sh X.Y.Z <64-character-source-sha256> ../homebrew-tap/Formula
```

Then run `brew audit --strict --online` and `brew install --build-from-source`
for each formula on Apple Silicon and Intel before publishing the tap commit.

- `betty-bluetooth` installs only `bt`.
- `betty-apple-audio` installs `bt-driver-apple-audio` on `PATH`.
- `betty-sony` installs `bt-driver-sony` on `PATH`.

Betty recognizes only those exact official executable names and verifies their
manifest IDs. Homebrew uninstall therefore removes support cleanly without a
post-install copy in the user's configuration directory. Driver state remains
user data and is deliberately preserved.
