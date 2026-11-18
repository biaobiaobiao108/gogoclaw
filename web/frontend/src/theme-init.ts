try {
  if (localStorage.getItem("theme") === "dark") {
    document.documentElement.classList.add("dark")
  }
} catch {}
