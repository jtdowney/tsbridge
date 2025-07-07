#!/bin/bash

# Script to clean up architecture-specific Docker image tags
# Usage: ./cleanup-arch-tags.sh [--dry-run] <tag-pattern>
# Example: ./cleanup-arch-tags.sh --dry-run "pr-123"
# Example: ./cleanup-arch-tags.sh "v0.7.0"

set -euo pipefail

# Configuration
REGISTRY="ghcr.io"
OWNER="jtdowney"
PACKAGE_NAME="tsbridge"
PACKAGE_TYPE="container"

# Parse arguments
DRY_RUN=false
if [ "$#" -eq 0 ]; then
    echo "Usage: $0 [--dry-run] <tag-pattern>"
    echo "Example: $0 --dry-run pr-123"
    echo "Example: $0 v0.7.0"
    exit 1
fi

if [ "$1" = "--dry-run" ]; then
    DRY_RUN=true
    shift
fi

TAG_PATTERN="${1:-}"

if [ -z "$TAG_PATTERN" ]; then
    echo "Error: Tag pattern is required"
    exit 1
fi

# Check for required environment variables
if [ -z "${GITHUB_TOKEN:-}" ]; then
    echo "Error: GITHUB_TOKEN environment variable is required"
    echo "Generate a token with 'delete:packages' permission at https://github.com/settings/tokens"
    exit 1
fi

echo "Configuration:"
echo "  Registry: $REGISTRY"
echo "  Owner: $OWNER"
echo "  Package: $PACKAGE_NAME"
echo "  Tag pattern: $TAG_PATTERN"
echo "  Dry run: $DRY_RUN"
echo ""

# Function to delete a specific version
delete_version() {
    local version_id=$1
    local tag=$2
    
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] Would delete version ID: ${version_id} (tag: ${tag})"
    else
        echo "Deleting version ID: ${version_id} (tag: ${tag})"
        response=$(curl -s -w "\n%{http_code}" -X DELETE \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${GITHUB_TOKEN}" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            "https://api.github.com/user/packages/${PACKAGE_TYPE}/${PACKAGE_NAME}/versions/${version_id}")
        
        http_code=$(echo "$response" | tail -n1)
        body=$(echo "$response" | sed '$d')
        
        if [ "$http_code" = "204" ]; then
            echo "  ✓ Successfully deleted"
        else
            echo "  ✗ Failed to delete (HTTP ${http_code})"
            if [ -n "$body" ]; then
                echo "  Response: $body"
            fi
        fi
    fi
}

# Architecture suffixes to look for
ARCH_SUFFIXES=("-amd64" "-arm64")

echo "Fetching package versions..."

# Get all versions (paginated)
page=1
all_versions=""

while true; do
    response=$(curl -s -w "\n%{http_code}" \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${GITHUB_TOKEN}" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/user/packages/${PACKAGE_TYPE}/${PACKAGE_NAME}/versions?per_page=100&page=${page}")
    
    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | sed '$d')
    
    if [ "$http_code" != "200" ]; then
        echo "Error fetching package versions (HTTP ${http_code})"
        echo "Response: $body"
        exit 1
    fi
    
    # Check if we got any results
    if [ "$(echo "$body" | jq -r '. | length')" -eq 0 ]; then
        break
    fi
    
    all_versions="${all_versions}${body}"
    page=$((page + 1))
done

# Process versions
echo ""
echo "Searching for architecture-specific tags matching pattern: ${TAG_PATTERN}"
echo ""

found_count=0
deleted_count=0

for suffix in "${ARCH_SUFFIXES[@]}"; do
    # Look for tags matching the pattern with architecture suffix
    tags_to_check=("${TAG_PATTERN}${suffix}")
    
    # If pattern looks like a version tag, also check "latest" variants
    if [[ "$TAG_PATTERN" =~ ^v[0-9] ]]; then
        tags_to_check+=("latest${suffix}")
    fi
    
    for tag in "${tags_to_check[@]}"; do
        # Find version IDs for this tag
        version_ids=$(echo "$all_versions" | jq -r ".[] | select(.metadata.container.tags[]? == \"${tag}\") | .id")
        
        if [ -n "$version_ids" ]; then
            while IFS= read -r version_id; do
                found_count=$((found_count + 1))
                delete_version "$version_id" "$tag"
                if [ "$DRY_RUN" = false ]; then
                    deleted_count=$((deleted_count + 1))
                fi
            done <<< "$version_ids"
        fi
    done
done

echo ""
echo "Summary:"
echo "  Found: ${found_count} architecture-specific tags"
if [ "$DRY_RUN" = true ]; then
    echo "  This was a dry run. No tags were deleted."
    echo "  Run without --dry-run to actually delete the tags."
else
    echo "  Deleted: ${deleted_count} tags"
fi