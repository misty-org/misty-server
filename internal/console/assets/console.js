// Sidebar search: filters console pages by title; Enter opens the first match.
document.addEventListener("DOMContentLoaded", () => {
  const input = document.getElementById("nav-search");
  if (!input) return;
  const groups = [...document.querySelectorAll(".nav-group")];
  const filter = () => {
    const query = input.value.trim().toLowerCase();
    for (const group of groups) {
      let visible = 0;
      for (const item of group.querySelectorAll(".nav-item")) {
        const match = item.textContent.toLowerCase().includes(query);
        item.hidden = !match;
        if (match) visible++;
      }
      group.hidden = visible === 0;
    }
  };
  input.addEventListener("input", filter);
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") {
      const first = document.querySelector(".nav-item:not([hidden])");
      if (first) window.location.href = first.getAttribute("href");
    } else if (event.key === "Escape") {
      input.value = "";
      filter();
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "/" && document.activeElement !== input && !/INPUT|TEXTAREA/.test(document.activeElement.tagName)) {
      event.preventDefault();
      input.focus();
    }
  });
});
