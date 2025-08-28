#!/usr/bin/env python3
"""
Generate status table from metadata.yaml for the InfluxDB Reader Receiver.

This script reads the metadata.yaml file and generates a status table
similar to the ones used in OpenTelemetry Collector Contrib components.
"""

import sys
import re
from pathlib import Path

def read_metadata(metadata_path):
    """Read metadata from YAML file using simple parsing."""
    try:
        with open(metadata_path, 'r') as f:
            content = f.read()
        
        # Simple YAML parsing for our specific format
        metadata = {}
        lines = content.split('\n')
        
        for line in lines:
            line = line.strip()
            if line.startswith('type:'):
                metadata['type'] = line.split(':', 1)[1].strip()
            elif line.startswith('alpha:'):
                metadata['stability'] = {'alpha': ['metrics']}
            elif line.startswith('distributions:'):
                # Extract distributions from the line
                dist_match = re.search(r'\[(.*?)\]', line)
                if dist_match:
                    distributions = [d.strip() for d in dist_match.group(1).split(',')]
                    metadata['distributions'] = distributions
        
        return metadata
    except FileNotFoundError:
        print(f"Error: {metadata_path} not found")
        sys.exit(1)
    except Exception as e:
        print(f"Error parsing {metadata_path}: {e}")
        sys.exit(1)

def get_stability_badge(stability):
    """Generate stability badge markdown."""
    if 'alpha' in stability:
        return "![Alpha](https://img.shields.io/badge/Stability-Alpha-orange)"
    elif 'beta' in stability:
        return "![Beta](https://img.shields.io/badge/Stability-Beta-yellow)"
    elif 'stable' in stability:
        return "![Stable](https://img.shields.io/badge/Stability-Stable-green)"
    else:
        return "![Undefined](https://img.shields.io/badge/Stability-Undefined-lightgrey)"

def get_stability_level(stability):
    """Get stability level text."""
    if 'alpha' in stability:
        return "Alpha"
    elif 'beta' in stability:
        return "Beta"
    elif 'stable' in stability:
        return "Stable"
    else:
        return "Undefined"

def get_distributions(distributions):
    """Format distributions list."""
    if not distributions:
        return "None"
    return ", ".join([f"[{d}](https://github.com/open-telemetry/opentelemetry-collector-{d})" for d in distributions])

def generate_status_table(metadata):
    """Generate status table markdown."""
    stability = metadata.get('stability', {})
    distributions = metadata.get('distributions', [])
    
    badge = get_stability_badge(stability)
    level = get_stability_level(stability)
    dist_text = get_distributions(distributions)
    
    table = f"""| Status                   | Stability Level | Distributions |
| ------------------------ | --------------- | ------------- |
| {badge} | {level} | {dist_text} |

"""
    
    # Add stability description
    if 'alpha' in stability:
        table += """**Status**: This receiver is in **Alpha** stage and is not yet stable. Breaking changes may occur in future releases.

**Stability**: The receiver is marked as Alpha for metrics. This means:
- The API may change in future releases
- Breaking changes are possible
- Not recommended for production use without thorough testing
- Feedback and contributions are welcome

"""
    elif 'beta' in stability:
        table += """**Status**: This receiver is in **Beta** stage and is approaching stability.

**Stability**: The receiver is marked as Beta for metrics. This means:
- The API is mostly stable but may have minor changes
- Breaking changes are unlikely but possible
- Suitable for testing in production environments
- Feedback is still welcome

"""
    elif 'stable' in stability:
        table += """**Status**: This receiver is **Stable** and ready for production use.

**Stability**: The receiver is marked as Stable for metrics. This means:
- The API is stable and unlikely to change
- Breaking changes will only occur in major versions
- Recommended for production use
- Long-term support is provided

"""
    
    table += f"**Distributions**: Available in the {dist_text} distribution(s) of the OpenTelemetry Collector.\n"
    
    return table

def main():
    """Main function."""
    # Find metadata.yaml file
    metadata_path = Path("receiver/influxdbreaderreceiver/metadata.yaml")
    
    if not metadata_path.exists():
        print(f"Error: {metadata_path} not found")
        sys.exit(1)
    
    # Read metadata
    metadata = read_metadata(metadata_path)
    
    # Generate status table
    status_table = generate_status_table(metadata)
    
    # Output the table
    print(status_table)

if __name__ == "__main__":
    main()
