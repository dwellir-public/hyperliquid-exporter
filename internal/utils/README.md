# utils

Shared file-resolution helpers used across monitors.

- `LatestFile(root)`: newest file under a date/hour/height-named tree, resolved by descending into the greatest-named entry at each level (`os.ReadDir`, no full walk).
- `LatestFileCache`: rate-limits `LatestFile` for tailers that poll in tight EOF loops.
