# Pending NEWS entries

Each pull request adds its NEWS entry here as its own file, instead of
editing NEWS, so that several open pull requests don't change the same
lines. The file is named after the branch, with `/` replaced by `-`
(`fix/hostname-buffer` -> `fix-hostname-buffer.md`). Its first line is the
NEWS section the entry belongs to; the rest is the entry in NEWS's wording:

```
Bug fixes:
* The host name buffer ...
```

The release pull request moves all entries into the NEWS section of the
release and removes the files (this README stays). See `.claude/CLAUDE.md`.
