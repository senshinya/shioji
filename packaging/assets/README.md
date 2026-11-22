# Shioji app icon

Selected concept: A, tidal path (潮路). Generated with the built-in imagegen tool, then converted to standard ICNS sizes with the native macOS silhouette renderer. `Shioji-source.png` retains the unmodified selected concept; `Shioji.icns` is the packaged asset.

`Shioji-iOS.png` uses the same artwork for iPhone, iPad, and TestFlight. Run `swift packaging/make-app-icon.swift packaging/assets --ios` to produce the opaque 1024 × 1024 sRGB asset from the center of the concept. The renderer deepens the graphite tone for the iOS Home Screen and keeps the ivory tile color.

Generation prompt: one macOS rounded-square icon for Shioji, ivory tile with a dark graphite flowing S-shaped sea channel between two shore forms; restrained cool monochrome, flat graphic treatment, no letters or typography. The image is application identity, not an extra logo in the main UI.
