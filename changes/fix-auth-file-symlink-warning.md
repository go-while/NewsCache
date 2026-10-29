Bug fixes:
* The warning about an unsafe `Authentication file` no longer fires for every symbolic link. It now follows the link and warns, under the configured path, when the target is group- or world-writable (it used to miss that), and warns once when the file or the link target cannot be inspected. Each configured path is reported once, at startup and after a reload.
