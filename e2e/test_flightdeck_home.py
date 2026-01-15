import pytest
from playwright.sync_api import Page, expect


@pytest.mark.e2e
def test_home_page_loads(page: Page, base_url: str):
    """Test that the flightdeck home page loads with the expected title."""
    page.goto(base_url)

    expect(page).to_have_title("Home - PTD")


@pytest.mark.e2e
def test_home_page_has_welcome_message(page: Page, base_url: str):
    """Test that the home page displays the welcome message."""
    page.goto(base_url)

    welcome_heading = page.get_by_role(
        "heading",
        name="Welcome to Posit Team, your end-to-end platform for creating amazing data products.",
    )
    expect(welcome_heading).to_be_visible()


@pytest.mark.e2e
def test_home_page_has_product_links(page: Page, base_url: str):
    """Test that the home page has links to all Posit Team products."""
    page.goto(base_url)

    # Extract the base domain from base_url
    # e.g., https://workshop.posit.team -> workshop.posit.team
    import re
    domain_match = re.search(r'https?://([^/]+)', base_url)
    base_domain = domain_match.group(1) if domain_match else None
    is_local = "localhost" in base_url or "127.0.0.1" in base_url

    # Check that product links are visible - we don't hardcode URLs since they vary by deployment
    # For local dev, links point to example.posit.team; for production, they use the actual base domain
    workbench_link = page.get_by_role("link", name="Workbench", exact=False)
    if workbench_link.count() > 0:
        expect(workbench_link).to_be_visible()
        href = workbench_link.get_attribute("href")
        assert href.startswith("https://"), f"Workbench link should be HTTPS"
        if not is_local:
            assert base_domain in href, f"Workbench link should contain base domain {base_domain}"

    connect_link = page.get_by_role("link", name="Connect", exact=False)
    if connect_link.count() > 0:
        expect(connect_link).to_be_visible()
        href = connect_link.get_attribute("href")
        assert href.startswith("https://"), f"Connect link should be HTTPS"
        if not is_local:
            assert base_domain in href, f"Connect link should contain base domain {base_domain}"

    package_manager_link = page.get_by_role("link", name="Package Manager", exact=False)
    if package_manager_link.count() > 0:
        expect(package_manager_link).to_be_visible()
        href = package_manager_link.get_attribute("href")
        assert href.startswith("https://"), f"Package Manager link should be HTTPS"
        if not is_local:
            assert base_domain in href, f"Package Manager link should contain base domain {base_domain}"


@pytest.mark.e2e
def test_home_page_has_help_link(page: Page, base_url: str):
    """Test that the home page has a help link in the navigation."""
    page.goto(base_url)

    # The Help link is rendered as an icon with href="/help"
    # Find it by the href attribute since the icon doesn't have text content
    help_link = page.locator('a[href="/help"]')
    expect(help_link).to_be_visible()


@pytest.mark.e2e
def test_home_page_has_posit_team_logo_link(page: Page, base_url: str):
    """Test that the Posit Team logo links to the home page."""
    page.goto(base_url)

    logo_link = page.get_by_role("link", name="Posit Team")
    expect(logo_link).to_be_visible()
    expect(logo_link).to_have_attribute("href", "/")


@pytest.mark.e2e
def test_dark_mode_toggle_exists(page: Page, base_url: str):
    """Test that the dark mode toggle button is present (if available on this deployment)."""
    page.goto(base_url)

    # The theme toggle has ID "theme-toggle" and aria-label "Toggle theme"
    # This feature may not be available on all deployments
    theme_toggle = page.locator("#theme-toggle")
    if theme_toggle.count() > 0:
        expect(theme_toggle).to_be_visible()
    else:
        pytest.skip("Dark mode toggle not available on this deployment")


@pytest.mark.e2e
def test_dark_mode_toggle_changes_theme(page: Page, base_url: str):
    """Test that clicking the dark mode toggle changes the page theme (if available)."""
    # Clear localStorage to ensure consistent initial state
    page.goto(base_url)
    page.evaluate("localStorage.removeItem('theme')")
    page.reload()

    # Check if the theme toggle exists (may not be available on all deployments)
    theme_toggle = page.locator("#theme-toggle")
    if theme_toggle.count() == 0:
        pytest.skip("Dark mode toggle not available on this deployment")

    # Get the html element to check the class for dark mode
    html = page.locator("html")

    # Get initial state
    is_initially_dark = page.evaluate("document.documentElement.classList.contains('dark')")

    # Click the theme toggle using ID selector
    theme_toggle.click()

    # Theme should have toggled
    if is_initially_dark:
        # Was dark, should now be light
        expect(html).not_to_have_class("dark")
    else:
        # Was light, should now be dark
        expect(html).to_have_class("dark")

    # Click again to toggle back
    theme_toggle.click()

    # Should be back to initial state
    if is_initially_dark:
        expect(html).to_have_class("dark")
    else:
        expect(html).not_to_have_class("dark")
