// Admin page behaviour. Kept out of the templates because the CSP forbids
// inline scripts. Everything is driven by data- attributes.
document.addEventListener('DOMContentLoaded', () => {
  // Fill a slug field from a title until someone edits the slug by hand.
  document.querySelectorAll('[data-slug-source]').forEach((title) => {
    const slug = document.getElementById(title.dataset.slugSource);
    if (!slug) return;
    let touched = slug.value !== '';
    slug.addEventListener('input', () => { touched = true; });
    title.addEventListener('input', () => {
      if (touched) return;
      slug.value = title.value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
    });
  });

  // Filter a list by its items' data-title / data-slug.
  document.querySelectorAll('[data-filter-list]').forEach((input) => {
    const list = document.getElementById(input.dataset.filterList);
    if (!list) return;
    input.addEventListener('input', () => {
      const q = input.value.toLowerCase();
      list.querySelectorAll('li[data-title]').forEach((li) => {
        const hit = li.dataset.title.toLowerCase().includes(q) || (li.dataset.slug || '').toLowerCase().includes(q);
        li.hidden = !hit;
      });
    });
  });

  // Ask before destructive buttons and links.
  document.querySelectorAll('[data-confirm]').forEach((el) => {
    el.addEventListener('click', (e) => {
      if (!window.confirm(el.dataset.confirm)) e.preventDefault();
    });
  });
});
