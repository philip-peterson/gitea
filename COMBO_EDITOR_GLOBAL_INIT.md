# Initializing ComboMarkdownEditor via `data-global-init`

As part of the `data-global-init` migration effort, we need to move
ComboMarkdownEditor onto `data-global-init`. Today it relies on a one-time
startup sweep and a `CustomInit` opt-out flag. After the change, the editor
template marks its own root element, and the global observer sets up every
editor wherever and whenever it appears: in the initial page or in HTML added
later.

## How it works today

`initGlobalComboMarkdownEditor()` runs once at startup and sets up every
`.combo-markdown-editor:not(.custom-init)` already in the page. Templates that
pass `CustomInit` get the `custom-init` class, are skipped by the sweep, and
their page code calls `initComboMarkdownEditor()` itself.

So whoever includes the editor template has to choose correctly, and both
choices can go wrong:

- Flag it, and you must also write page code to set it up.
- Don't flag it, and the editor works only if it's in the page from the start.
  One added later, such as by AJAX, just sits there broken.
- Flag the wrong one, or set one up from both sides, and it crashes.

## Why we can't just add the attribute

A component on `data-global-init` has to meet two conditions:

1. **All its settings come from the element.** The observer only hands the
   handler the element, so there's nowhere else to pass settings in.
2. **Setting it up twice is harmless.** The observer and page code may both ask
   for the editor, in either order.

ComboMarkdownEditor fails both:

- **Settings:** the wiki page passes options to `initComboMarkdownEditor()` in
  JavaScript, including a `previewRender` function, which can't go in an HTML
  attribute.
- **Setting up twice:** the constructor throws `ComboMarkdownEditor already
  initialized`. For example, the issue edit form clones
  `#issue-comment-editor-template` into the page and then sets up the editor
  itself. With the attribute, the observer would set up the clone first, and
  the page code's call would then throw.

So `data-global-init` could only be added conditionally, on editors without
`CustomInit`, which keeps the flag and every way of getting it wrong.

## The change

Four commits. 1 and 2 are independent of each other. 3 builds on 2: idempotent
setup is only safe once options are gone, otherwise a later caller's options
would be silently ignored. 4 needs all three.

1. **Switch to `data-global-init`.** The startup sweep is replaced by a
   `data-global-init` handler. The attribute goes only on editors without
   `CustomInit`, so the flag still works as before, but editors added after page
   load are now set up too.
2. **Presets.** Settings that need JavaScript live in the component as named
   presets. The element chooses one by name (`data-editor-preset="wiki"`), and
   the `options` parameter is removed, so everything an editor needs can be read
   from its element.
3. **Setup is idempotent.** The first `initComboMarkdownEditor()` call stores its
   promise on the element, and every later caller gets back the same editor. The
   observer and page code can both ask without any coordination.
4. **Remove `CustomInit`.** With 2 and 3 in place, the attribute goes on every
   editor unconditionally and the flag is deleted. Page code that needs the
   editor still calls `initComboMarkdownEditor()` and gets the shared instance.
   This is the only commit that changes when editors are set up: the hidden
   reply editors move from first click to page load (see below).

## What you get

The editor template has one rule: include it and it works, whether at page load
or later, with or without page code that uses it. The broken and crashing cases
above can't happen anymore.

## What it costs

- **Startup cost.** `CustomInit` was also quietly making some editors lazy: the
  hidden reply box in every PR review conversation
  (`templates/repo/diff/conversation.tmpl`,
  `templates/repo/issue/view_content/conversation.tmpl`) was only set up when
  someone clicked Reply. Now they're all set up at page load, so a PR with 50
  review threads sets up 50 editors up front. That needs measuring. If it's too
  slow, the fix is lazy setup inside the `data-global-init` handler (for
  example, wait until the editor becomes visible), with no change for anyone who
  uses it.
- **Timing.** Editors are now set up a bit later in page load, by the observer's
  initial sweep after the page-specific code instead of before it. The three
  places that look up an editor directly (`repo-release.ts`, and two in
  `repo-issue-edit.ts`) all run on user actions, so they're unaffected.
- **Size.** It changes about ten files for no visible feature.

## Where we're going

None of this is in this change. With the editor on `data-global-init`, the code
around it can follow, and this is what that lets us rip out and improve.

### What we can rip out

- **Page-level editor wiring.** Two setup functions still find their editor
  from page code with a querySelector guard. These are exactly the targets of the
  `data-global-init` migration, and each one can become a handler on the element
  it owns:
  - `initRepoCommentForm()` (`repo-issue.ts`) finds `.comment.form`,
    then branches on whether it's an issue-template form. Its two branches
    become their own components: `initSingleCommentEditor()` on the comment
    form, and `initIssueTemplateCommentEditors()` on each issue-template field
    (`templates/repo/issue/fields/textarea.tmpl`). The branching goes away.
  - The issue edit form (`repo-issue-edit.ts`) clones
    `#issue-comment-editor-template` and then sets up the editor itself. Its
    editor-specific wiring can move into a handler on the editor's form.

  The wiki form already made this move (`data-global-init="initRepoWikiForm"`),
  so it's the model for the others.
- **Page code's use of `getComboMarkdownEditor()`.** It returns whatever is on
  the element, even an editor that hasn't finished setting up. Once every
  consumer lives in a `data-global-init` handler that awaits
  `initComboMarkdownEditor()`, page code's calls to it and their document-wide
  lookups (such as `#comment-form .combo-markdown-editor` in
  `repo-issue-edit.ts`) can go. The textarea back-pointer
  (`textarea._giteaComboMarkdownEditor`) is already unused after this change.

### What we can improve

- **Load the editor only on pages that have one.** `ComboMarkdownEditor.ts`
  pulls in the toolbar and text-expander elements, Tribute and Dropzone. Today
  every page loads it, because six modules import from it statically. Once the page
  wiring above is gone, the `data-global-init` handler can `import()` the
  editor on first use, the same way `colorpicker.ts` does, and pages without an
  editor don't pay for it.
- **Set up hidden editors lazily, if the numbers call for it.** If setting up
  the hidden reply editors at page load turns out to be slow, the handler can
  wait until the editor is visible. Because setup is idempotent, page code
  that calls `initComboMarkdownEditor()` doesn't change: it just triggers the
  setup early.
