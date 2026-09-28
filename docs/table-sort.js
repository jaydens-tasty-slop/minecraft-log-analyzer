document.querySelectorAll('table.sortable').forEach((table) => {
  const body = table.tBodies[0];
  const headers = Array.from(table.tHead.rows[0].cells);
  const controls = document.querySelector('[data-sort-controls="' + table.id + '"]');
  const filter = document.querySelector('[data-filter-table="' + table.id + '"]');
  const select = controls?.querySelector('select');
  const directionButton = controls?.querySelector('button');
  const sortRows = (column, direction, numeric) => {
    headers.forEach((item) => item.removeAttribute('aria-sort'));
    headers[column].setAttribute('aria-sort', direction === 'ascending' ? 'ascending' : 'descending');
    const rows = Array.from(body.rows);
    rows.sort((left, right) => {
      const a = left.cells[column].dataset.sort ?? left.cells[column].textContent.trim();
      const b = right.cells[column].dataset.sort ?? right.cells[column].textContent.trim();
      const comparison = numeric ? Number(a) - Number(b) : a.localeCompare(b, undefined, {sensitivity: 'base'});
      return direction === 'ascending' ? comparison : -comparison;
    });
    rows.forEach((row) => body.appendChild(row));
  };
  const syncControls = (column, direction) => {
    if (!select || !directionButton) return;
    select.value = String(column);
    directionButton.dataset.direction = direction === 'ascending' ? 'asc' : 'desc';
    directionButton.textContent = direction === 'ascending' ? 'Ascending' : 'Descending';
  };
  const updateHash = (column, direction) => {
    const key = headers[column].querySelector('button')?.dataset.key;
    if (!key) return;
    const fragment = new URLSearchParams({
      sort: key,
      direction: direction === 'ascending' ? 'asc' : 'desc'
    });
    history.replaceState(null, '', '#' + fragment.toString());
  };
  const applySort = (column, direction, updateURL) => {
    const button = headers[column]?.querySelector('button');
    if (!button) return;
    sortRows(column, direction, button.dataset.type === 'number');
    syncControls(column, direction);
    if (updateURL) updateHash(column, direction);
  };
  headers.forEach((header, column) => {
    const button = header.querySelector('button');
    if (!button) return;
    button.addEventListener('click', () => {
      const current = header.getAttribute('aria-sort');
      const direction = current === 'ascending' ? 'descending' : 'ascending';
      applySort(column, direction, true);
    });
  });
  if (select && directionButton) {
    const applyMobileSort = () => {
      const direction = directionButton.dataset.direction === 'asc' ? 'ascending' : 'descending';
      applySort(Number(select.value), direction, true);
    };
    select.addEventListener('change', applyMobileSort);
    directionButton.addEventListener('click', () => {
      directionButton.dataset.direction = directionButton.dataset.direction === 'asc' ? 'desc' : 'asc';
      applyMobileSort();
    });
  }
  if (filter) {
    filter.addEventListener('input', () => {
      const query = filter.value.trim().toLocaleLowerCase();
      Array.from(body.rows).forEach((row) => {
        const names = row.dataset.search ?? row.cells[0].textContent;
        row.hidden = query !== '' && !names.toLocaleLowerCase().includes(query);
      });
    });
  }
  const applyHash = () => {
    const fragment = new URLSearchParams(location.hash.slice(1));
    const key = fragment.get('sort');
    const shortDirection = fragment.get('direction');
    if (!key || (shortDirection !== 'asc' && shortDirection !== 'desc')) return;
    const column = headers.findIndex((header) => header.querySelector('button')?.dataset.key === key);
    if (column < 0) return;
    applySort(column, shortDirection === 'asc' ? 'ascending' : 'descending', false);
  };
  applyHash();
  window.addEventListener('hashchange', applyHash);
});
