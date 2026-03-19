<!-- mintlify
title: "Expo Integration"
description: "Automatically import builds from Expo Application Services (EAS)"
target: integrations/expo.mdx
-->

## Overview

The Expo integration enables:

- **Automatic build imports** - Pull builds directly from EAS
- **Version synchronization** - New EAS builds appear in Revyl automatically
- **Streamlined workflow** - Build once in Expo, test immediately in Revyl
- **Support for both iOS and Android** - Import both platform builds

![Expo Settings tab](/images/integrations/expo-settings-connected.png)

---

## Prerequisites

Before setting up the Expo integration, you need:

1. **An Expo account** - Sign up at [expo.dev](https://expo.dev)
2. **An Expo project** with EAS Build configured
3. **Robot User access** - Create a programmatic access token
4. **At least one EAS build** - Build your app using `eas build`

---

## Setup Guide

### Step 1: Create an Expo Robot User

Expo Robot Users are service accounts for programmatic access. They allow Revyl to access your builds without using your personal credentials.

1. **Log in to Expo**

   - Go to [expo.dev](https://expo.dev) and sign in

2. **Create a Robot User**

   - Navigate to your account settings
   - Go to **Access Tokens** or **Robot Users**
   - Click **Create Robot User**
   - Give it a descriptive name: `revyl-build-access`

3. **Generate Access Token**
   - Select the robot user you just created
   - Click **Create Token**
   - Set permissions: **Read** access to builds (minimum required)
   - Copy the generated token immediately (it won't be shown again!)

[Learn more about Expo Robot Users →](https://docs.expo.dev/accounts/programmatic-access/)

### Step 2: Configure Expo in Revyl

1. **Navigate to Integrations**
   - In Revyl, click **Integrations** in the sidebar
   - Click the **Expo Settings** tab

![Expo Settings page](/images/integrations/expo-settings-connected.png)

2. **Paste the Access Token**

   - Find the **Expo Robot User Access Token** field
   - Paste the token you copied from Expo
   - Click the eye icon to verify it's correct

3. **Save Changes**

   - Click **Save Changes**
   - Revyl will verify the token by making a test API call to Expo

4. **Success!**
   - If valid, you'll see a success message
   - Revyl can now access your Expo builds

### Step 3: Add an Expo Project

After configuring your token, add projects to import builds from:

1. In the **Expo Projects** section, click **+ Add Project**
2. Enter your Expo project slug (e.g., `@your-username/my-app`)
3. Click **Add**
4. The project appears in the list, enabling build imports

![Expo Settings with connected status](/images/integrations/expo-settings-connected.png)

### Step 4: Import Builds from the Builds Page

Once Expo is connected with at least one project:

1. Go to **Builds** in the sidebar
2. Select any build from the list
3. Click **Upload from Expo** button (top right of build detail panel)

![Builds page with Upload from Expo button](/images/builds/builds-page-expo-button.png)

4. In the modal, select your Expo project from the dropdown

![Expo import modal](/images/integrations/expo-import-modal.png)

5. Click **Fetch** to load available EAS builds

![Expo import modal with build list](/images/integrations/expo-import-builds-list.png)

6. Click a build to select it
7. Click **Import Build** to add it as a new version

<Note>
  Builds are filtered by platform - iOS builds only show iOS EAS builds, Android shows Android builds.
</Note>

---

## How It Works

### Build Synchronization

Once configured, Revyl periodically checks Expo for new builds:

1. **You run `eas build`** in your Expo project
2. **EAS builds your app** (iOS/Android)
3. **Revyl detects the new build** (within ~5 minutes)
4. **Build appears in Revyl** under the Builds page
5. **You can now create tests** using that build

### Build Matching

Revyl matches Expo builds to Revyl builds using:

- **App identifier** - Bundle ID (iOS) or Package Name (Android)
- **Version** - App version string
- **Build number** - Build number from app.json or app.config.js

### Supported Build Types

Revyl supports importing:

- ✅ **iOS builds** - zipped `.app` files from EAS
- ✅ **Android builds** - `.apk`  files from EAS
- ✅ **Development builds** - With Expo Dev Client
- ✅ **Production builds** - Release builds for testing before distribution

---

## Managing Expo Integration

### Viewing Connection Status

To check if Expo is connected:

1. Go to **Integrations** → **Expo Settings**
2. If a token is configured, you'll see:
   - The token field is filled (hidden by default - click eye icon to reveal)
   - **Save Changes** button is disabled (no changes)
3. If disconnected:
   - The field is empty
   - You'll see the "Enter Expo access token" placeholder

### Updating the Token

To change or refresh the Expo token:

1. Generate a new token in Expo (follow Step 1 above)
2. Go to **Integrations** → **Expo Settings**
3. Clear the existing token and paste the new one
4. Click **Save Changes**

**Note:** Updating the token does NOT affect existing builds in Revyl. Only new imports will use the new token.

### Disconnecting Expo

To disconnect Expo from Revyl:

1. Go to **Integrations** → **Expo Settings**
2. Clear the **Expo Robot User Access Token** field
3. Click **Save Changes**
4. Revyl will no longer sync new builds from Expo

**Existing builds remain in Revyl** even after disconnecting. They won't be deleted.

---

## Build Import Workflow

### Typical Expo + Revyl Workflow

1. **Develop your app**

   ```bash
   cd my-expo-app
   npm install
   ```

2. **Build with EAS**

   ```bash
   eas build --platform android --profile preview
   ```

3. **Wait for build to complete**

   - Monitor progress in the terminal or at [expo.dev/builds](https://expo.dev/builds)

4. **Build appears in Revyl automatically**

   - Go to Revyl **Builds** page
   - See the new build listed
   - Or create a test and select it from the build dropdown

5. **Create and run tests**
   - Use the imported build in your tests
   - No need to manually download/upload!


---

## Best Practices

### Token Security

- **Use Robot Users, not personal tokens** - Avoids tying builds to individual accounts
- **Don't commit tokens to Git** - Store in environment variables or secrets
- **Rotate tokens periodically** - Generate new tokens every 6-12 months
- **Revoke tokens you're not using** - Clean up old tokens in Expo settings

### Build Profiles

Configure different EAS Build profiles for different purposes:

```json
// eas.json
{
  "build": {
    "development": {
      "developmentClient": true,
      "distribution": "internal"
    },
    "preview": {
      "distribution": "internal",
      "android": { "buildType": "apk" }
    },
    "production": {
      "distribution": "store"
    }
  }
}
```

**Use `preview` profile for Revyl testing:**

```bash
eas build --platform android --profile preview
```

This creates an APK suitable for automated testing.

### Versioning

Use semantic versioning in your `app.json`:

```json
{
  "expo": {
    "version": "1.2.3",
    "android": {
      "versionCode": 10203
    },
    "ios": {
      "buildNumber": "1.2.3"
    }
  }
}
```

This helps track which builds correspond to which app versions in Revyl.

---

## Hot Reload with Expo

For rapid development iteration, use hot reload to test against your local Expo dev server. This enables near-instant testing—JavaScript changes are reflected immediately without rebuilding.

### Quick Setup

1. **Build a development client** with `expo-dev-client`:
   ```bash
   npx eas build --profile development --platform ios
   ```

2. **Upload to Revyl** as a build:
   ```bash
   revyl build upload --file <path-to-build> --name "ios-dev-client"
   ```

3. **Configure hot reload**:
   ```bash
   revyl init --provider expo
   ```

4. **Run tests with hot reload**:
   ```bash
   revyl dev test run login-flow
   ```

<Note>
  Hot reload requires a development client build with `expo-dev-client` installed. Production builds won't work with hot reload.
</Note>

See [Dev Loop](/cli/dev-loop-guide) for high-level usage patterns (including coding-agent verification loops), and [Hot Reload](/cli/hot-reload) for full setup and troubleshooting.

---

## Next Steps

- [Upload builds manually →](/builds/index)
- [Create a test →](/test-creation)
- [Set up GitHub integration →](/integrations/github)
- [Automate with CI/CD →](/ci-cd/github-actions)

---

Need help? Contact **support@revyl.ai**
