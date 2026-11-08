const gitkeep = new URL("../../backend/dist/.gitkeep", import.meta.url)

await Bun.write(
  gitkeep,
  "# Keep the embedded web backend dist directory in version control.\n",
)
