Feature: Hiding a plugin's admin screens from a caller
  A plugin can answer whether its admin menu entries and screens show for
  the caller its context carries, for example not before the caller has
  set up what the screens work on. An application asks every plugin that
  answers, through one filter hook, and leaves out the screens of the
  plugins it lists. A plugin that does not answer always shows its
  screens. An answer that fails, panics or comes after the hook's budget
  ran out hides nothing, so a broken plugin never takes a working screen
  away, and the report names its plugin.

  Scenario: The plugins whose screens hide are listed in registration order
    Given the plugins "notes", "feed" and "mail" answer whether their screens show
    And the plugin "theme" is registered without answering it
    And "notes" hides its screens
    And "mail" hides its screens
    When the application asks which plugins hide their screens
    Then the plugins that hide their screens are "notes" and "mail"

  Scenario: An answer that fails or panics hides nothing
    Given the plugins "mail", "archive" and "notes" answer whether their screens show
    And "mail" hides its screens
    And "mail" fails with "mailbox unreadable"
    And "archive" hides its screens
    And "notes" hides its screens
    And "notes" panics with "boom"
    When the application asks which plugins hide their screens
    Then the plugins that hide their screens are "archive"
    And the report holds "pluginkit: mail admin.screens: mailbox unreadable"
    And the report holds "pluginkit: notes admin.screens panicked: boom" with the stack of the panic

  Scenario: An answer that comes after the budget ran out hides nothing
    Given the plugins "mail" and "notes" answer whether their screens show
    And the hook has a budget of 2 ms
    And "mail" takes 5 ms
    And "mail" hides its screens
    And "notes" hides its screens
    When the application asks which plugins hide their screens
    Then no plugin hides its screens
    And the report marks the failure of "mail" as caused by the ended context
    And the report marks the failure of "notes" as caused by the ended context
