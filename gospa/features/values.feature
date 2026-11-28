Feature: Server values in the page of a built app
  A program often holds values its app needs before it asks the server
  anything, such as where the API lives or how long a write may take.
  GoSPA writes each value into the page head as a meta element, which the
  app reads with document.querySelector. The values go right where the
  head opens, before anything the page holds, in the order the caller
  gave them. Each name and value is escaped, so no value can end its
  element or add markup. Only the page carries the values, whichever path
  it answers.

  Background:
    Given a built app whose page reads:
      """
      <!doctype html>
      <html lang="en">
        <head>
          <title>App</title>
        </head>
        <body><div id="root"></div></body>
      </html>
      """

  Scenario: Each value is written as a meta element where the head opens
    Given the app carries the value "api-root" set to "/api/"
    And the app carries the value "write-timeout" set to "30"
    When a browser asks for "/posts/42"
    Then the answer reads:
      """
      <!doctype html>
      <html lang="en">
        <head><meta name="api-root" content="/api/"><meta name="write-timeout" content="30">
          <title>App</title>
        </head>
        <body><div id="root"></div></body>
      </html>
      """

  Scenario: A value that holds markup is escaped
    Given the app carries the value "notice" set to:
      """
      say "hi" & <b>bye</b>
      """
    When a browser asks for "/"
    Then the answer holds:
      """
      <meta name="notice" content="say &#34;hi&#34; &amp; &lt;b&gt;bye&lt;/b&gt;">
      """

  Scenario: A page with no head tag gets the values where its head would start
    Given the build writes "index.html" again as:
      """
      <!doctype html><title>App</title><div id="root"></div>
      """
    And the app carries the value "api-root" set to "/api/"
    When a browser asks for "/"
    Then the answer reads:
      """
      <!doctype html><meta name="api-root" content="/api/"><title>App</title><div id="root"></div>
      """

  Scenario: Asking for index.html by name gets the page with its values
    Given the app carries the value "api-root" set to "/api/"
    When a browser asks for "/index.html"
    Then the answer is the page
    And the answer holds:
      """
      <meta name="api-root" content="/api/">
      """

  Scenario: A browser that kept the page from before a change of values gets the new page
    Given the app carries the value "write-timeout" set to "30"
    When a browser asks for "/"
    And the program starts again with the value "write-timeout" set to "60"
    And the browser asks for "/" again with the tag it got
    Then the answer is the page
    And the answer holds:
      """
      <meta name="write-timeout" content="60">
      """

  Scenario: Without values the page is sent as the build wrote it
    When a browser asks for "/"
    Then the answer reads:
      """
      <!doctype html>
      <html lang="en">
        <head>
          <title>App</title>
        </head>
        <body><div id="root"></div></body>
      </html>
      """

  Scenario: No other HTML file carries the values
    Given the build writes "help.html" as:
      """
      <!doctype html><title>Help</title>
      """
    And the app carries the value "api-root" set to "/api/"
    When a browser asks for "/help.html"
    Then the answer reads:
      """
      <!doctype html><title>Help</title>
      """
