Feature: Calling the plugins that implement a hook
  A hook is a named moment where an application calls every plugin that
  implements one interface, in the order the plugins were registered. An
  action tells each plugin that something happened, and a filter passes a
  value through each plugin. One firing of a hook shares one time budget
  among all its plugins. A plugin that fails, panics or runs slow never
  stops the plugins after it, and the counters name the plugin at fault.

  @wip
  Scenario: Every plugin that implements the hook is called in order
    Given the plugins "analytics", "feed" and "seo-basics" implement the hook "public.head-tags"
    And the plugin "theme" is registered without implementing it
    When the hook fires
    Then "analytics", "feed" and "seo-basics" are called in that order
    And "theme" is not called

  @wip
  Scenario: A failing plugin leaves the plugins after it running
    Given the plugins "analytics", "feed" and "seo-basics" implement the hook "content.changed"
    And "feed" fails with "index unavailable"
    When the hook fires
    Then "analytics", "feed" and "seo-basics" are called in that order
    And the report holds "pluginkit: feed content.changed: index unavailable"
    And "feed" is counted 1 error

  @wip
  Scenario: A panicking plugin leaves the plugins after it running
    Given the plugins "analytics", "feed" and "seo-basics" implement the hook "content.changed"
    And "feed" panics with "boom"
    When the hook fires
    Then "analytics", "feed" and "seo-basics" are called in that order
    And the report holds "pluginkit: feed content.changed panicked: boom" with the stack of the panic
    And "feed" is counted 1 panic

  @wip
  Scenario: The plugins after a slow one are still called
    Given the plugins "analytics", "feed" and "seo-basics" implement the hook "public.head-tags"
    And the hook has a budget of 2 ms
    And "analytics" takes 5 ms
    When the hook fires
    Then "analytics", "feed" and "seo-basics" are called in that order
    And "feed" and "seo-basics" are called after the deadline passed
    And the hook is counted 1 overrun

  @wip
  Scenario: An error caused by the ended deadline is not the plugin's fault
    Given the plugins "analytics" and "search" implement the hook "content.changed"
    And the hook has a budget of 2 ms
    And "analytics" takes 5 ms
    And "search" fails with the error of its ended context
    When the hook fires
    Then "search" is counted 0 errors and 1 call whose context ended
    And the report marks the failure of "search" as caused by the ended context

  @wip
  Scenario: An error of its own after the deadline still counts against the plugin
    Given the plugins "analytics" and "search" implement the hook "content.changed"
    And the hook has a budget of 2 ms
    And "analytics" takes 5 ms
    And "search" fails with "index unavailable"
    When the hook fires
    Then "search" is counted 1 error and 0 calls whose context ended

  @wip
  Scenario: A plugin that hides the context's error behind its own text is counted an error
    Given the plugins "analytics" and "search" implement the hook "content.changed"
    And the hook has a budget of 2 ms
    And "analytics" takes 5 ms
    And "search" fails with the text of its ended context but not the error itself
    When the hook fires
    Then "search" is counted 1 error and 0 calls whose context ended

  @wip
  Scenario: The firing that runs over its budget charges no plugin
    Given the plugins "analytics", "reviews" and "feed" implement the hook "public.head-tags"
    And the hook has a budget of 2 ms and times each plugin for 20 firings after an overrun
    And "analytics" takes 3 ms
    When the hook fires
    Then the hook is counted 1 overrun
    And no plugin is charged an overrun

  @wip
  Scenario: A timed overrun is charged to the slowest plugin
    Given the plugins "analytics", "reviews" and "feed" implement the hook "public.head-tags"
    And the hook has a budget of 2 ms and times each plugin for 20 firings after an overrun
    And the hook has run over its budget once
    When the hook fires while "analytics" takes 1.9 ms, "reviews" takes 0.7 ms and "feed" takes 0.1 ms
    Then "analytics" is charged 1 overrun
    And "reviews" and "feed" are charged no overrun

  @wip
  Scenario: Each plugin is timed only for the firings after an overrun
    Given the plugins "analytics" and "feed" implement the hook "public.head-tags"
    And the hook has a budget of 2 ms and times each plugin for 3 firings after an overrun
    And the hook has run over its budget once
    When the hook fires 5 times within its budget
    Then "analytics" and "feed" are each timed in 3 firings

  @wip
  Scenario: A filter passes the value through every plugin
    Given the plugins "brand" and "suffix" implement the filter hook "page.title"
    And "brand" puts "Example: " before the title
    And "suffix" puts " | Home" after the title
    When the hook filters the title "Hello"
    Then the title becomes "Example: Hello | Home"

  @wip
  Scenario: A filter keeps the incoming value when a plugin fails or panics
    Given the plugins "brand", "broken", "loud" and "suffix" implement the filter hook "page.title"
    And "brand" puts "Example: " before the title
    And "broken" fails with "no title"
    And "loud" panics with "boom"
    And "suffix" puts " | Home" after the title
    When the hook filters the title "Hello"
    Then the title becomes "Example: Hello | Home"
