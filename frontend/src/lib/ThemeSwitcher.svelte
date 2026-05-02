<!--
  Native <select> theme picker. Bound to the theme module — onchange
  applies the new theme via dataset attribute and persists to
  localStorage. The list is driven by THEMES; adding a new theme to
  that array surfaces it here automatically.
-->
<script>
  import { THEMES, applyTheme, getCurrentTheme } from './theme';

  let { current = $bindable(getCurrentTheme()) } = $props();

  function onChange(event) {
    const id = event.target.value;
    applyTheme(id);
    current = id;
  }
</script>

<select
  class="theme-switcher"
  value={current}
  onchange={onChange}
  aria-label="Select theme"
>
  {#each THEMES as t (t.id)}
    <option value={t.id}>{t.label}</option>
  {/each}
</select>

<style>
  .theme-switcher {
    background: var(--bg-elevated);
    color: var(--text-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    padding: 0.4rem 1.85rem 0.4rem 0.75rem;
    font-size: 0.9rem;
    cursor: pointer;
    appearance: none;
    background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'><path fill='none' stroke='%238a8a8e' stroke-width='1.5' stroke-linecap='round' stroke-linejoin='round' d='M3 5l3 3 3-3'/></svg>");
    background-repeat: no-repeat;
    background-position: right 0.6rem center;
    background-size: 12px;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .theme-switcher:hover {
    border-color: var(--border-strong);
  }
  .theme-switcher:focus {
    outline: none;
    border-color: var(--border-focus);
    box-shadow: 0 0 0 3px var(--accent-bg);
  }
</style>
