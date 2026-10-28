#!/usr/bin/env bash
# One-time setup so GitHub Actions can deploy to Cloud Run without a key.
# Creates a deployer service account, a Workload Identity pool and provider
# trusting this repository's GitHub OIDC tokens, grants the deployer the
# roles that `gcloud run deploy --source` needs, and stores the two
# identifiers the workflow reads as repository secrets.
set -euo pipefail
PROJECT=${PROJECT:-relay-513017}
REPO=${REPO:-hacka-tron/Relay}
SA_NAME=relay-deployer
SA="$SA_NAME@$PROJECT.iam.gserviceaccount.com"
PN=$(gcloud projects describe "$PROJECT" --format='value(projectNumber)')

gcloud services enable iamcredentials.googleapis.com sts.googleapis.com --project "$PROJECT"

gcloud iam service-accounts describe "$SA" --project "$PROJECT" >/dev/null 2>&1 ||
  gcloud iam service-accounts create "$SA_NAME" --project "$PROJECT" --display-name "GitHub Actions deployer"

# Roles for deploying from source: build with Cloud Build, push to Artifact
# Registry, upload the source tarball, create the revision, and run it as
# the service's runtime account.
for role in roles/run.admin roles/cloudbuild.builds.editor roles/artifactregistry.writer \
            roles/storage.admin roles/iam.serviceAccountUser roles/serviceusage.serviceUsageConsumer \
            roles/logging.viewer; do
  gcloud projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$SA" --role "$role" --quiet >/dev/null
done

gcloud iam workload-identity-pools describe github --location global --project "$PROJECT" >/dev/null 2>&1 ||
  gcloud iam workload-identity-pools create github --location global --project "$PROJECT" --display-name "GitHub Actions"

gcloud iam workload-identity-pools providers describe github --location global --workload-identity-pool github --project "$PROJECT" >/dev/null 2>&1 ||
  gcloud iam workload-identity-pools providers create-oidc github --location global --workload-identity-pool github --project "$PROJECT" \
    --issuer-uri https://token.actions.githubusercontent.com \
    --attribute-mapping "google.subject=assertion.sub,attribute.repository=assertion.repository" \
    --attribute-condition "assertion.repository == '$REPO'"

# Only workflows from this repository may act as the deployer.
gcloud iam service-accounts add-iam-policy-binding "$SA" --project "$PROJECT" --role roles/iam.workloadIdentityUser \
  --member "principalSet://iam.googleapis.com/projects/$PN/locations/global/workloadIdentityPools/github/attribute.repository/$REPO" --quiet >/dev/null

gh secret set GCP_WIF_PROVIDER --repo "$REPO" --body "projects/$PN/locations/global/workloadIdentityPools/github/providers/github"
gh secret set GCP_DEPLOY_SA --repo "$REPO" --body "$SA"
echo "done: GitHub Actions in $REPO can now deploy to Cloud Run in $PROJECT as $SA"
