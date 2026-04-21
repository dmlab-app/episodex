/**
 * EpisodeX - TV Series Audio Track Manager
 * Hash-based SPA with 5 pages: Series List, Series Detail, Season Detail, Updates, Next Seasons
 */

const state = {
    series: [],
    updates: [],
    recommendations: [],
    currentView: null,
    currentSeriesId: null,
    currentSeasonNum: null,
};

// TMDB TV genre IDs → human-readable names. Recommendations API returns raw IDs.
const TMDB_TV_GENRES = {
    10759: 'Action & Adventure',
    16: 'Animation',
    35: 'Comedy',
    80: 'Crime',
    99: 'Documentary',
    18: 'Drama',
    10751: 'Family',
    10762: 'Kids',
    9648: 'Mystery',
    10763: 'News',
    10764: 'Reality',
    10765: 'Sci-Fi & Fantasy',
    10766: 'Soap',
    10767: 'Talk',
    10768: 'War & Politics',
    37: 'Western',
};

// Inline SVG placeholder for series without posters
const PLACEHOLDER_SVG = `data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 450" fill="none"><rect width="300" height="450" fill="#1a1c22"/><rect x="1" y="1" width="298" height="448" stroke="#323640" stroke-width="2" fill="none"/><g transform="translate(150, 180)"><circle cx="0" cy="0" r="60" stroke="#3d4250" stroke-width="3" fill="none"/><circle cx="0" cy="0" r="20" stroke="#3d4250" stroke-width="3" fill="none"/><line x1="0" y1="-20" x2="0" y2="-60" stroke="#3d4250" stroke-width="3"/><line x1="0" y1="20" x2="0" y2="60" stroke="#3d4250" stroke-width="3"/><line x1="-20" y1="0" x2="-60" y2="0" stroke="#3d4250" stroke-width="3"/><line x1="20" y1="0" x2="60" y2="0" stroke="#3d4250" stroke-width="3"/><line x1="-14" y1="-14" x2="-42" y2="-42" stroke="#3d4250" stroke-width="3"/><line x1="14" y1="14" x2="42" y2="42" stroke="#3d4250" stroke-width="3"/><line x1="-14" y1="14" x2="-42" y2="42" stroke="#3d4250" stroke-width="3"/><line x1="14" y1="-14" x2="42" y2="-42" stroke="#3d4250" stroke-width="3"/><circle cx="0" cy="-40" r="8" fill="#282c37"/><circle cx="0" cy="40" r="8" fill="#282c37"/><circle cx="-40" cy="0" r="8" fill="#282c37"/><circle cx="40" cy="0" r="8" fill="#282c37"/><circle cx="-28" cy="-28" r="8" fill="#282c37"/><circle cx="28" cy="28" r="8" fill="#282c37"/><circle cx="-28" cy="28" r="8" fill="#282c37"/><circle cx="28" cy="-28" r="8" fill="#282c37"/></g><text x="150" y="320" font-family="system-ui, sans-serif" font-size="48" font-weight="bold" fill="#3d4250" text-anchor="middle">?</text><text x="150" y="380" font-family="system-ui, sans-serif" font-size="18" fill="#3d4250" text-anchor="middle">No Poster</text></svg>')}`;

function posterSrc(url) {
    const src = url || PLACEHOLDER_SVG;
    // Sanitize: only allow http(s) and data URIs to prevent XSS via crafted URLs
    if (src.startsWith('http://') || src.startsWith('https://') || src.startsWith('data:')) {
        return src;
    }
    return PLACEHOLDER_SVG;
}

// Escape URL for use in CSS url('...') context to prevent CSS injection
function cssUrl(url) {
    return posterSrc(url).replace(/'/g, "\\'").replace(/\)/g, '%29');
}

// HTML escape utility to prevent XSS
function esc(str) {
    if (str == null) return '';
    const div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
}

// ==============================================================================
// API
// ==============================================================================
const api = {
    async get(url) {
        const res = await fetch(url);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async post(url, data) {
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async put(url, data) {
        const res = await fetch(url, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(data)
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    },
    async delete(url) {
        const res = await fetch(url, { method: 'DELETE' });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
    }
};

// ==============================================================================
// Toast Notifications
// ==============================================================================
function showToast(message, type = 'success') {
    const container = document.getElementById('toast-container');
    const toast = document.createElement('div');
    toast.className = `toast toast-${type}`;
    toast.innerHTML = `<span class="toast-message">${esc(message)}</span>`;
    container.appendChild(toast);
    setTimeout(() => {
        toast.classList.add('hiding');
        setTimeout(() => toast.remove(), 300);
    }, 3000);
}

// ==============================================================================
// Routing
// ==============================================================================
function router() {
    const hash = window.location.hash.slice(1) || '/';

    if (hash === '/' || hash === '/series') {
        showSeriesListPage();
    } else if (hash.match(/^\/series\/(\d+)$/)) {
        const match = hash.match(/^\/series\/(\d+)$/);
        const seriesId = parseInt(match[1]);
        showSeriesDetailPage(seriesId);
    } else if (hash.match(/^\/series\/(\d+)\/season\/(\d+)$/)) {
        const match = hash.match(/^\/series\/(\d+)\/season\/(\d+)$/);
        const seriesId = parseInt(match[1]);
        const seasonNum = parseInt(match[2]);
        showSeasonDetailPage(seriesId, seasonNum);
    } else if (hash === '/updates') {
        showUpdatesPage();
    } else if (hash === '/seasons') {
        showSeasonsPage();
    } else if (hash === '/recommendations') {
        showRecommendationsPage();
    } else if (hash === '/add-series') {
        showAddSeriesPage();
    }
}

function navigate(path) {
    window.location.hash = path;
}

// ==============================================================================
// Page 1: Series List
// ==============================================================================
async function showSeriesListPage() {
    state.currentView = 'series';
    hideAllPages();
    document.getElementById('page-series').classList.add('active');
    updateNav('series');
    await loadSeries();
}

async function loadSeries() {
    const grid = document.getElementById('series-grid');
    const loading = document.getElementById('loading-state');
    const empty = document.getElementById('empty-state');

    loading.style.display = 'flex';
    grid.innerHTML = '';

    try {
        state.series = await api.get('/api/series');
        loading.style.display = 'none';

        if (state.series.length === 0) {
            empty.style.display = 'flex';
            return;
        }
        empty.style.display = 'none';
        renderSeries();
        updateStats();
    } catch (e) {
        loading.style.display = 'none';
        showToast(`Failed to load series: ${e.message || e}`, 'error');
    }
}

function renderSeries() {
    const grid = document.getElementById('series-grid');
    const searchQuery = document.getElementById('search-input').value.toLowerCase();
    const filter = document.querySelector('.filter-btn.active')?.dataset.filter || 'all';
    const sort = document.getElementById('sort-select').value;

    let list = [...state.series];

    // Filter
    if (filter === 'continuing') list = list.filter(s => s.status?.toLowerCase() === 'continuing');
    else if (filter === 'ended') list = list.filter(s => s.status?.toLowerCase() === 'ended');

    // Search
    if (searchQuery) {
        list = list.filter(s =>
            s.title.toLowerCase().includes(searchQuery) ||
            (s.original_title && s.original_title.toLowerCase().includes(searchQuery))
        );
    }

    // Sort
    if (sort === 'title') list.sort((a, b) => a.title.localeCompare(b.title));
    else if (sort === 'title-desc') list.sort((a, b) => b.title.localeCompare(a.title));
    else list.sort((a, b) => new Date(b.created_at) - new Date(a.created_at));

    grid.innerHTML = list.map(s => {
        const hasNoMatch = !s.tvdb_id;
        return `
        <div class="series-card ${hasNoMatch ? 'unmatched' : ''}" onclick="navigate('/series/${s.id}')">
            <div class="series-poster">
                <img src="${esc(posterSrc(s.poster_url))}" alt="${esc(s.title)}" loading="lazy">
                ${hasNoMatch ? '<div class="unmatched-overlay"></div>' : ''}
            </div>
            <div class="series-info">
                <h4 class="series-title">${esc(s.title)}</h4>
                <div class="series-meta">
                    <span>${s.downloaded_seasons || 0} seasons</span>
                    <span class="series-status-badge ${esc(s.status)}">${esc(s.status)}</span>
                </div>
                ${hasNoMatch ? `
                    <button class="btn btn-match" onclick="event.stopPropagation(); openMatchModal(${s.id})">
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                        </svg>
                        Match
                    </button>
                ` : ''}
            </div>
        </div>
        `;
    }).join('');
}

function updateStats() {
    document.getElementById('stat-total').textContent = state.series.length;
    document.getElementById('stat-continuing').textContent = state.series.filter(s => s.status?.toLowerCase() === 'continuing').length;
    document.getElementById('stat-ended').textContent = state.series.filter(s => s.status?.toLowerCase() === 'ended').length;
    document.getElementById('stat-seasons').textContent = state.series.reduce((sum, s) => sum + (s.downloaded_seasons || 0), 0);
}

// ==============================================================================
// Page 2: Series Detail
// ==============================================================================
async function showSeriesDetailPage(seriesId) {
    state.currentView = 'series-detail';
    state.currentSeriesId = seriesId;
    hideAllPages();
    document.getElementById('page-series-detail').classList.add('active');
    updateNav('series');
    await loadSeriesDetail(seriesId);
}

async function loadSeriesDetail(seriesId) {
    try {
        const series = await api.get(`/api/series/${seriesId}`);

        // Hero backdrop
        const backdropEl = document.getElementById('series-hero-backdrop');
        if (series.backdrop_url) {
            backdropEl.style.backgroundImage = `url('${cssUrl(series.backdrop_url)}')`;
        } else if (series.poster_url) {
            backdropEl.style.backgroundImage = `url('${cssUrl(series.poster_url)}')`;
        } else {
            backdropEl.style.backgroundImage = 'none';
        }

        // Poster & title
        document.getElementById('detail-series-title').textContent = series.title;
        document.getElementById('detail-original-title').textContent = series.original_title || '';
        document.getElementById('detail-series-poster').src = posterSrc(series.poster_url);

        // Metadata tags
        const tagsEl = document.getElementById('detail-tags');
        let tagsHTML = `<span id="detail-series-status" class="series-status-badge ${esc(series.status || 'unknown')}">${esc(series.status || 'unknown')}</span>`;
        if (series.year) tagsHTML += `<span class="meta-tag">${esc(series.year)}</span>`;
        if (series.content_rating) tagsHTML += `<span class="meta-tag">${esc(series.content_rating)}</span>`;
        if (series.runtime) tagsHTML += `<span class="meta-tag">${esc(series.runtime)} min</span>`;
        if (series.rating) tagsHTML += `<span class="meta-tag meta-tag-star">${series.rating.toFixed(1)}</span>`;
        if (series.genres && series.genres.length > 0) {
            series.genres.forEach(g => {
                const name = typeof g === 'string' ? g : (g.name || g);
                tagsHTML += `<span class="meta-tag">${esc(name)}</span>`;
            });
        }
        if (series.networks && series.networks.length > 0) {
            series.networks.forEach(n => {
                const name = typeof n === 'string' ? n : (n.name || n);
                tagsHTML += `<span class="meta-tag meta-tag-network">${esc(name)}</span>`;
            });
        }
        tagsEl.innerHTML = tagsHTML;

        // Overview
        const overviewEl = document.getElementById('detail-overview');
        overviewEl.textContent = series.overview || '';
        overviewEl.style.display = series.overview ? 'block' : 'none';

        // Characters
        const charsSection = document.getElementById('series-characters');
        const charsRow = document.getElementById('characters-row');
        if (series.characters && series.characters.length > 0) {
            charsSection.style.display = 'block';
            charsRow.innerHTML = series.characters.map(c => `
                <div class="character-card">
                    <div class="character-avatar">
                        <img src="${esc(posterSrc(c.image_url))}" alt="${esc(c.character_name || '')}" loading="lazy">
                    </div>
                    <div class="character-name">${esc(c.character_name || '')}</div>
                    <div class="character-actor">${esc(c.actor_name || '')}</div>
                </div>
            `).join('');
        } else {
            charsSection.style.display = 'none';
        }

        // Load seasons
        const seasons = await api.get(`/api/series/${seriesId}/seasons`);
        renderSeasons(series, seasons);

    } catch (e) {
        showToast(`Failed to load series detail: ${e.message || e}`, 'error');
        console.error(e);
    }
}

function renderSeasons(series, seasons) {
    const grid = document.getElementById('seasons-grid');

    grid.innerHTML = seasons.map(season => {
        const owned = season.on_disk === true;
        const seasonImage = season.image || series.poster_url || PLACEHOLDER_SVG;

        if (!owned) {
            // Locked season
            return `
                <div class="season-card locked" data-season="${season.season_number}">
                    <div class="season-poster">
                        <img src="${esc(seasonImage)}" alt="Season ${season.season_number}" loading="lazy">
                        <div class="season-overlay">
                            <div class="season-lock-icon">
                                <svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                                    <rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect>
                                    <path d="M7 11V7a5 5 0 0 1 10 0v4"></path>
                                </svg>
                            </div>
                            <div class="season-number">Season ${season.season_number}</div>
                            <span class="not-available-badge">Not Available</span>
                        </div>
                    </div>
                </div>
            `;
        } else {
            // Owned season - clickable card with optional voice badge
            const voiceBadge = season.track_name
                ? `<span class="voice-badge">${esc(season.track_name)}</span>`
                : '';
            return `
                <div class="season-card on-disk" data-season="${season.season_number}" onclick="navigate('/series/${series.id}/season/${season.season_number}')">
                    <div class="season-poster">
                        <img src="${esc(seasonImage)}" alt="Season ${season.season_number}" loading="lazy">
                        <div class="season-overlay">
                            <div class="season-number">Season ${season.season_number}</div>
                            ${voiceBadge}
                        </div>
                    </div>
                </div>
            `;
        }
    }).join('');
}


async function deleteSeries() {
    if (!state.currentSeriesId) return;
    const series = state.series.find(s => s.id === state.currentSeriesId);
    if (!confirm(`Delete "${series?.title}" and all its files from disk? This cannot be undone.`)) return;

    try {
        await api.delete(`/api/series/${state.currentSeriesId}`);
        showToast('Series deleted');
        navigate('/series');
    } catch (e) {
        showToast(`Failed to delete series: ${e.message || e}`, 'error');
    }
}

// ==============================================================================
// Page 3: Season Detail (Audio Cutter)
// ==============================================================================
async function showSeasonDetailPage(seriesId, seasonNum) {
    state.currentView = 'season-detail';
    state.currentSeriesId = seriesId;
    state.currentSeasonNum = seasonNum;

    hideAllPages();
    const page = document.getElementById('page-season-detail');
    page.classList.add('active');

    // Reset page state
    document.getElementById('season-detail-title').textContent = `Season ${seasonNum}`;
    document.getElementById('season-detail-subtitle').textContent = '';
    document.getElementById('season-not-on-disk').style.display = 'none';
    document.getElementById('voice-selector-panel').style.display = 'flex';
    document.getElementById('audio-tracks-container').style.display = 'none';
    document.getElementById('season-files-box').style.display = 'block';
    document.getElementById('files-list').innerHTML = '<div class="loader"></div>';
    document.getElementById('audio-tracks-list').innerHTML = '';
    document.getElementById('progress-container').style.display = 'none';
    document.getElementById('tracker-link-container').style.display = 'none';
    document.getElementById('tracker-link').removeAttribute('href');

    updateNav('series');

    // Load season data
    await loadSeasonDetail(seriesId, seasonNum);
}

let selectedTrackName = null;
let currentAudioPlayer = null;
let voiceSelectHandler = null;

loadSeasonDetail._trackerId = 0;
async function loadSeasonDetail(seriesId, seasonNum) {
    // Invalidate any in-flight tracker request immediately, before any await
    const trackerLoadId = ++loadSeasonDetail._trackerId;

    try {
        // Load series info and season info in parallel
        const [series, seasonInfo] = await Promise.all([
            api.get(`/api/series/${seriesId}`),
            api.get(`/api/series/${seriesId}/seasons/${seasonNum}`)
        ]);

        document.getElementById('season-detail-subtitle').textContent = series.title;

        // Fetch tracker link (non-blocking)
        const trackerContainer = document.getElementById('tracker-link-container');
        const trackerLink = document.getElementById('tracker-link');
        trackerContainer.style.display = 'none';
        trackerLink.removeAttribute('href');
        api.get(`/api/series/${seriesId}/seasons/${seasonNum}/tracker`)
            .then(data => {
                if (trackerLoadId !== loadSeasonDetail._trackerId) return; // stale response
                if (data.tracker_url) {
                    trackerLink.href = data.tracker_url;
                    trackerContainer.style.display = 'flex';
                }
            })
            .catch(() => {}); // silently ignore tracker errors

        // Check if season is owned (files present on disk)
        if (!seasonInfo.on_disk) {
            document.getElementById('season-not-on-disk').style.display = 'block';
            document.getElementById('voice-selector-panel').style.display = 'none';
            document.getElementById('season-files-box').style.display = 'none';
            return;
        }

        // Load audio tracks
        const audioData = await api.get(`/api/series/${seriesId}/seasons/${seasonNum}/audio`);

        // Display files
        const filesList = document.getElementById('files-list');
        if (audioData.files && audioData.files.length > 0) {
            filesList.innerHTML = audioData.files.map(file => `
                <div class="file-item ${file.processed ? 'processed' : ''}">
                    <span class="file-name">${esc(file.name)}</span>
                    <span class="file-status ${file.processed ? 'processed' : 'pending'}">
                        ${file.processed ? 'Processed' : 'Pending'}
                    </span>
                </div>
            `).join('');
        } else {
            filesList.innerHTML = '<p>No MKV files found</p>';
        }

        // Display audio tracks
        if (audioData.audio_tracks && audioData.audio_tracks.length > 0) {
            document.getElementById('audio-tracks-container').style.display = 'block';
            const tracksList = document.getElementById('audio-tracks-list');

            // Disable actions if season is already being processed
            if (audioData.processing) {
                const processBtn = document.getElementById('process-audio-btn');
                const defaultBtn = document.getElementById('set-default-btn');
                if (processBtn) { processBtn.disabled = true; processBtn.textContent = 'Processing...'; }
                if (defaultBtn) { defaultBtn.disabled = true; }
            }

            // Store file path for preview
            window.currentAudioFilePath = audioData.files[0]?.path;

            // Auto-select: prefer current season's track_name, then first history match, then first track
            let autoSelectIndex = 0;
            const currentTrack = seasonInfo.track_name;
            if (currentTrack) {
                const idx = audioData.audio_tracks.findIndex(t => t.name === currentTrack);
                if (idx >= 0) autoSelectIndex = idx;
            } else {
                const histIdx = audioData.audio_tracks.findIndex(t => t.matched_history);
                if (histIdx >= 0) autoSelectIndex = histIdx;
            }

            tracksList.innerHTML = audioData.audio_tracks.map((track, index) => `
                <div class="track-item ${index === autoSelectIndex ? 'selected' : ''} ${track.matched_history ? 'history-match' : ''}" id="track-item-${index}">
                    <input type="radio" name="audioTrack" value="${esc(track.name)}" id="track-radio-${index}"
                        ${index === autoSelectIndex ? 'checked' : ''} onchange="selectTrack('${esc(track.name)}', ${index})">
                    <div class="track-info" onclick="selectTrack('${esc(track.name)}', ${index}); document.getElementById('track-radio-${index}').checked = true;">
                        <div class="track-name">
                            ${esc(track.name) || 'Unnamed track'}
                            ${track.default ? ' <span class="default-badge">default</span>' : ''}
                            ${track.matched_history ? ' <span class="history-badge">used before</span>' : ''}
                        </div>
                        <div class="track-details">
                            Lang: ${esc(track.language)} | Codec: ${esc(track.codec)} | Channels: ${esc(track.channels) || 'N/A'}
                            | Files: ${track.file_count}/${track.total_files}${track.file_count < track.total_files ? ' ⚠' : ''}
                        </div>
                    </div>
                    <div class="track-actions">
                        <button class="btn-listen" id="listen-btn-${index}" onclick="event.stopPropagation(); togglePreview(${index})">
                            Listen
                        </button>
                    </div>
                    <div class="audio-player-wrapper" id="player-${index}" style="display: none;"></div>
                </div>
            `).join('');

            selectedTrackName = audioData.audio_tracks[autoSelectIndex]?.name || null;

            // Populate voice selector dropdown from track names
            const studioNames = parseStudiosFromTracks(audioData.audio_tracks);

            const voiceSelect = document.getElementById('voice-select');
            if (voiceSelect && studioNames.length > 0) {
                voiceSelect.innerHTML = '<option value="">No track selected</option>';
                studioNames.forEach(name => {
                    const option = document.createElement('option');
                    option.value = name;
                    option.textContent = name;
                    if (seasonInfo.track_name && seasonInfo.track_name === name) {
                        option.selected = true;
                    }
                    voiceSelect.appendChild(option);
                });
                if (voiceSelectHandler) {
                    voiceSelect.removeEventListener('change', voiceSelectHandler);
                }
                voiceSelectHandler = () => {
                    const name = voiceSelect.value || null;
                    updateSeasonVoice(seriesId, seasonNum, name);
                };
                voiceSelect.addEventListener('change', voiceSelectHandler);
            } else {
                document.getElementById('voice-selector-panel').style.display = 'none';
            }
        }

    } catch (e) {
        showToast(`Failed to load season detail: ${e.message || e}`, 'error');
        console.error(e);
    }
}

// Parse studio names from audio track names.
// Tracks like "Dub | HDRezka Studio", "DUB SomeName" → extract the studio part.
// Only considers Russian language tracks.
function parseStudiosFromTracks(tracks) {
    const studios = [];
    const seen = new Set();

    for (const track of tracks) {
        if (track.language !== 'rus' || !track.name) continue;

        let name = track.name.trim();

        // "Dub | HDRezka Studio" → "HDRezka Studio"
        if (name.includes('|')) {
            name = name.split('|').pop().trim();
        } else {
            // "DUB SomeName" → "SomeName"
            name = name.replace(/^dub\s+/i, '').trim();
        }

        if (name && !seen.has(name)) {
            seen.add(name);
            studios.push(name);
        }
    }

    return studios;
}

async function updateSeasonVoice(seriesId, seasonNum, trackName) {
    try {
        await api.put(`/api/series/${seriesId}/seasons/${seasonNum}`, {
            track_name: trackName
        });
        showToast('Track updated');
    } catch (e) {
        showToast('Failed to update track', 'error');
        console.error(e);
    }
}

function selectTrack(trackName, index) {
    selectedTrackName = trackName;
    document.querySelectorAll('.track-item').forEach((item, i) => {
        item.classList.toggle('selected', i === index);
    });
}

async function togglePreview(index) {
    const button = document.getElementById(`listen-btn-${index}`);
    const playerDiv = document.getElementById(`player-${index}`);

    // If already playing, stop and hide
    if (playerDiv.style.display !== 'none') {
        // Stop audio
        const audio = playerDiv.querySelector('audio');
        if (audio) {
            audio.pause();
            audio.currentTime = 0;
        }
        playerDiv.innerHTML = '';
        playerDiv.style.display = 'none';
        button.textContent = 'Listen';
        button.classList.remove('playing');
        currentAudioPlayer = null;
        return;
    }

    // Stop any other playing audio first
    document.querySelectorAll('.audio-player-wrapper').forEach((p, i) => {
        if (i !== index && p.style.display !== 'none') {
            const audio = p.querySelector('audio');
            if (audio) audio.pause();
            p.innerHTML = '';
            p.style.display = 'none';
            const btn = document.getElementById(`listen-btn-${i}`);
            if (btn) {
                btn.textContent = 'Listen';
                btn.classList.remove('playing');
            }
        }
    });

    // Show loading state
    button.disabled = true;
    button.textContent = 'Loading...';
    button.classList.add('loading');

    try {
        const filePath = window.currentAudioFilePath;
        if (!filePath) {
            throw new Error('No file available for preview');
        }

        const result = await api.post(`/api/series/${state.currentSeriesId}/seasons/${state.currentSeasonNum}/audio/preview`, {
            file_path: filePath,
            track_index: index
        });

        // Create audio player
        playerDiv.innerHTML = `
            <audio controls autoplay>
                <source src="${esc(result.preview_url)}" type="audio/mpeg">
                Your browser does not support audio playback.
            </audio>
        `;
        playerDiv.style.display = 'block';

        currentAudioPlayer = playerDiv.querySelector('audio');

        // Update button to "Stop" state
        button.textContent = 'Stop';
        button.classList.remove('loading');
        button.classList.add('playing');

        // When audio ends, reset button
        currentAudioPlayer.addEventListener('ended', () => {
            button.textContent = 'Listen';
            button.classList.remove('playing');
        });

        // When audio is paused manually via controls
        currentAudioPlayer.addEventListener('pause', () => {
            if (currentAudioPlayer.currentTime >= currentAudioPlayer.duration - 0.1) {
                // Audio ended naturally
                button.textContent = 'Listen';
                button.classList.remove('playing');
            }
        });

    } catch (e) {
        playerDiv.innerHTML = '<p class="preview-error">Failed to generate preview</p>';
        playerDiv.style.display = 'block';
        showToast('Failed to generate preview', 'error');
        button.textContent = 'Listen';
        button.classList.remove('loading');
    } finally {
        button.disabled = false;
    }
}

async function processSeasonAudio() {
    if (!selectedTrackName) {
        showToast('Please select an audio track', 'error');
        return;
    }

    const keepOriginal = document.getElementById('keep-original-checkbox').checked;

    if (!confirm('Process all files in this season? This will remove unwanted audio tracks.')) {
        return;
    }

    // Hide tracks container, show progress
    document.getElementById('audio-tracks-container').style.display = 'none';
    document.getElementById('progress-container').style.display = 'block';

    try {
        const response = await fetch(`/api/series/${state.currentSeriesId}/seasons/${state.currentSeasonNum}/audio/process`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                track_name: selectedTrackName,
                keep_original: keepOriginal
            })
        });

        if (!response.ok) {
            throw new Error('Failed to start processing');
        }

        // Read SSE stream
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let stats = { success: 0, error: 0, skipped: 0 };

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n\n');
            buffer = lines.pop(); // Keep incomplete line in buffer

            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    try {
                        const data = JSON.parse(line.slice(6));
                        handleProgressEvent(data, stats);
                    } catch (e) {
                        console.error('Failed to parse SSE event:', line, e);
                    }
                }
            }
        }

        showToast('Processing completed!');
        // Reload season page to show updated file statuses
        setTimeout(() => loadSeasonDetail(state.currentSeriesId, state.currentSeasonNum), 2000);

    } catch (e) {
        document.getElementById('progress-container').style.display = 'none';
        document.getElementById('audio-tracks-container').style.display = 'block';
        showToast('Processing failed', 'error');
    }
}

async function setDefaultTrack() {
    if (!selectedTrackName) {
        showToast('Please select an audio track', 'error');
        return;
    }

    if (!confirm('Set selected track as default for all files in this season?')) {
        return;
    }

    // Hide tracks container, show progress
    document.getElementById('audio-tracks-container').style.display = 'none';
    document.getElementById('progress-container').style.display = 'block';

    try {
        const response = await fetch(`/api/series/${state.currentSeriesId}/seasons/${state.currentSeasonNum}/audio/set-default`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ track_name: selectedTrackName })
        });

        if (!response.ok) {
            throw new Error('Failed to start set-default');
        }

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let stats = { success: 0, error: 0, skipped: 0 };

        while (true) {
            const { done, value } = await reader.read();
            if (done) break;

            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n\n');
            buffer = lines.pop();

            for (const line of lines) {
                if (line.startsWith('data: ')) {
                    try {
                        const data = JSON.parse(line.slice(6));
                        handleProgressEvent(data, stats);
                    } catch (e) {
                        console.error('Failed to parse SSE event:', line, e);
                    }
                }
            }
        }

        showToast('Default track set successfully!');
        setTimeout(() => showSeasonDetail(state.currentSeriesId, state.currentSeasonNum), 2000);

    } catch (e) {
        document.getElementById('progress-container').style.display = 'none';
        document.getElementById('audio-tracks-container').style.display = 'block';
        showToast('Failed to set default track', 'error');
    }
}

function handleProgressEvent(data, stats) {
    const progressBar = document.getElementById('progress-bar');
    const progressPercent = document.getElementById('progress-percent');

    // Find file item in the Files list by name and update its badge
    function updateFileBadge(fileName, statusClass, statusText) {
        const fileItems = document.querySelectorAll('#files-list .file-item');
        for (const item of fileItems) {
            const nameEl = item.querySelector('.file-name');
            if (nameEl && nameEl.textContent === fileName) {
                const badge = item.querySelector('.file-status');
                if (badge) {
                    badge.className = 'file-status ' + statusClass;
                    badge.textContent = statusText;
                }
                break;
            }
        }
    }

    switch (data.type) {
        case 'start':
            document.getElementById('progress-text').textContent = `Processing ${data.total} files...`;
            break;

        case 'progress': {
            // File started but not done yet — show (current-1)/total progress
            const percent = Math.round(((data.current - 1) / data.total) * 100);
            progressBar.style.width = percent + '%';
            progressPercent.textContent = percent + '%';
            updateFileBadge(data.file, 'processing', 'Processing...');
            break;
        }

        case 'file_done': {
            if (data.status === 'success') stats.success++;
            else if (data.status === 'error') stats.error++;
            else if (data.status === 'skipped') stats.skipped++;

            const statusText = data.status === 'success' ? 'Success' :
                              data.status === 'skipped' ? 'Skipped' : 'Error';
            updateFileBadge(data.file, data.status, statusText);

            const percentDone = Math.round((data.current / data.total) * 100);
            progressBar.style.width = percentDone + '%';
            progressPercent.textContent = percentDone + '%';
            break;
        }

        case 'complete':
            progressBar.style.width = '100%';
            progressBar.classList.add('complete');
            progressPercent.textContent = '100%';
            document.getElementById('progress-text').textContent = 'Complete!';
            break;
    }
}

// ==============================================================================
// Updates Page
// ==============================================================================
async function showUpdatesPage() {
    state.currentView = 'updates';
    hideAllPages();
    document.getElementById('page-updates').classList.add('active');
    updateNav('updates');
    await loadUpdates();
}

async function loadUpdates() {
    const list = document.getElementById('updates-list');
    const empty = document.getElementById('updates-empty');

    try {
        state.updates = await api.get('/api/updates');

        if (state.updates.length === 0) {
            list.innerHTML = '';
            empty.style.display = 'flex';
            updateBadge(0);
            return;
        }

        empty.style.display = 'none';
        updateBadge(state.updates.length);

        list.innerHTML = state.updates.map(u => {
            const newSeasons = u.new_seasons || [];
            const seasonLabel = newSeasons.length > 0
                ? newSeasons.map(s => `S${String(s.season_number).padStart(2, '0')}: ${s.aired_episodes} ep`).join(', ')
                : 'New seasons';
            return `
            <div class="update-card" onclick="navigate('/series/${u.id}')">
                <div class="update-poster">
                    <img src="${esc(posterSrc(u.poster_url))}" alt="${esc(u.title)}">
                </div>
                <div class="update-info">
                    <h4 class="update-title">${esc(u.title)}</h4>
                    <p class="update-detail"><span class="update-season">${esc(seasonLabel)}</span></p>
                </div>
            </div>
            `;
        }).join('');
    } catch (e) {
        showToast(`Failed to load updates: ${e.message || e}`, 'error');
    }
}

function updateBadge(count) {
    const badge = document.getElementById('updates-badge');
    if (count > 0) {
        badge.textContent = count;
        badge.style.display = 'flex';
    } else {
        badge.style.display = 'none';
    }
}

async function checkUpdates() {
    const btn = document.getElementById('check-updates-btn');
    btn.disabled = true;
    try {
        await api.post('/api/updates/check');
        showToast('Checking for updates...');
        // Background check runs async on the server; wait before refreshing
        await new Promise(r => setTimeout(r, 5000));
        await loadUpdates();
        showToast('Update check complete');
    } catch (e) {
        showToast('Update check failed', 'error');
    } finally {
        btn.disabled = false;
    }
}

// ==============================================================================
// Next Seasons Page
// ==============================================================================
async function showSeasonsPage() {
    state.currentView = 'seasons';
    hideAllPages();
    document.getElementById('page-seasons').classList.add('active');
    updateNav('seasons');
    await loadNextSeasons();
}

async function loadNextSeasons() {
    const list = document.getElementById('seasons-download-list');
    const empty = document.getElementById('seasons-empty');
    const loading = document.getElementById('seasons-loading');

    list.innerHTML = '';
    empty.style.display = 'none';
    loading.style.display = 'flex';

    try {
        const data = await api.get('/api/next-seasons');

        loading.style.display = 'none';

        if (data.length === 0) {
            empty.style.display = 'flex';
            updateSeasonsBadge(0);
            return;
        }

        updateSeasonsBadge(data.length);

        list.innerHTML = data.map(item => {
            const seasonLabel = `Season ${item.next_season}`;
            const sizeLabel = item.torrent_size ? esc(item.torrent_size) : '';
            const torrentTitle = item.torrent_title ? esc(item.torrent_title) : '';
            const safeUrl = item.tracker_url && (item.tracker_url.startsWith('http://') || item.tracker_url.startsWith('https://')) ? item.tracker_url : '';
            const hasLink = !!safeUrl;

            return `
            <div class="season-download-card">
                <div class="season-download-poster" onclick="navigate('/series/${parseInt(item.id, 10)}')">
                    <img src="${esc(posterSrc(item.poster_url))}" alt="${esc(item.title)}">
                </div>
                <div class="season-download-info">
                    <h4 class="season-download-title" onclick="navigate('/series/${parseInt(item.id, 10)}')">${esc(item.title)}</h4>
                    <p class="season-download-season">${esc(seasonLabel)}</p>
                    ${torrentTitle ? `<p class="season-download-torrent">${torrentTitle}</p>` : ''}
                    ${sizeLabel ? `<p class="season-download-size">${sizeLabel}</p>` : ''}
                </div>
                ${hasLink ? `
                <a href="${esc(safeUrl)}" class="season-download-link" target="_blank" rel="noopener noreferrer" title="Open on Kinozal">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>
                        <polyline points="15 3 21 3 21 9"/>
                        <line x1="10" y1="14" x2="21" y2="3"/>
                    </svg>
                </a>
                ` : `
                <div class="season-download-link season-download-link-disabled" title="No torrent found">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <circle cx="11" cy="11" r="8"/>
                        <path d="M21 21l-4.35-4.35"/>
                    </svg>
                </div>
                `}
            </div>
            `;
        }).join('');
    } catch (e) {
        loading.style.display = 'none';
        showToast(`Failed to load next seasons: ${e.message || e}`, 'error');
    }
}

function updateSeasonsBadge(count) {
    const badge = document.getElementById('seasons-badge');
    if (count > 0) {
        badge.textContent = count;
        badge.style.display = 'flex';
    } else {
        badge.style.display = 'none';
    }
}

// ==============================================================================
// Recommendations Page
// ==============================================================================
async function showRecommendationsPage() {
    state.currentView = 'recommendations';
    hideAllPages();
    document.getElementById('page-recommendations').classList.add('active');
    updateNav('recommendations');
    await loadRecommendations();
}

async function loadRecommendations() {
    const list = document.getElementById('recommendations-list');
    const empty = document.getElementById('recommendations-empty');
    const loading = document.getElementById('recommendations-loading');

    list.innerHTML = '';
    empty.style.display = 'none';
    loading.style.display = 'flex';

    try {
        state.recommendations = await api.get('/api/recommendations');
        loading.style.display = 'none';

        if (state.recommendations.length === 0) {
            empty.style.display = 'flex';
            return;
        }

        renderRecommendations();
    } catch (e) {
        loading.style.display = 'none';
        showToast(`Failed to load recommendations: ${e.message || e}`, 'error');
    }
}

function renderRecommendations() {
    const list = document.getElementById('recommendations-list');

    // Attach delegated click handler once
    if (!list._blacklistHandlerAttached) {
        list.addEventListener('click', e => {
            const btn = e.target.closest('.btn-blacklist');
            if (!btn) return;
            e.preventDefault();
            e.stopPropagation();
            const tvdbID = parseInt(btn.dataset.tvdbId, 10);
            const title = btn.dataset.title || '';
            if (tvdbID > 0) blacklistRecommendation(tvdbID, title);
        });
        list._blacklistHandlerAttached = true;
    }

    list.innerHTML = state.recommendations.map(r => {
        const safeUrl = r.tracker_url && (r.tracker_url.startsWith('http://') || r.tracker_url.startsWith('https://')) ? r.tracker_url : '';
        const rating = typeof r.rating === 'number' && r.rating > 0 ? r.rating.toFixed(1) : null;
        const genreNames = (r.genres || [])
            .map(id => TMDB_TV_GENRES[id])
            .filter(Boolean)
            .slice(0, 3);

        return `
        <div class="recommendation-card" data-tvdb-id="${parseInt(r.tvdb_id, 10)}">
            <a href="${esc(safeUrl)}" class="recommendation-link" target="_blank" rel="noopener noreferrer">
                <div class="recommendation-poster">
                    <img src="${esc(posterSrc(r.poster_url))}" alt="${esc(r.title)}" loading="lazy">
                    ${rating ? `<span class="recommendation-rating">★ ${rating}</span>` : ''}
                </div>
                <div class="recommendation-info">
                    <h4 class="recommendation-title">${esc(r.title)}</h4>
                    <div class="recommendation-meta">
                        ${r.year ? `<span>${esc(r.year)}</span>` : ''}
                        ${r.torrent_size ? `<span class="recommendation-size">${esc(r.torrent_size)}</span>` : ''}
                    </div>
                    ${genreNames.length > 0 ? `
                        <div class="recommendation-genres">
                            ${genreNames.map(g => `<span class="recommendation-genre">${esc(g)}</span>`).join('')}
                        </div>
                    ` : ''}
                </div>
            </a>
            <button class="btn-blacklist" title="Blacklist this show" data-tvdb-id="${parseInt(r.tvdb_id, 10)}" data-title="${esc(r.title || '')}">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"/>
                    <line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
            </button>
        </div>
        `;
    }).join('');
}

async function blacklistRecommendation(tvdbID, title) {
    try {
        await api.post('/api/recommendations/blacklist', { tvdb_id: tvdbID, title });
        state.recommendations = state.recommendations.filter(r => r.tvdb_id !== tvdbID);
        const card = document.querySelector(`.recommendation-card[data-tvdb-id="${tvdbID}"]`);
        if (card) card.remove();
        if (state.recommendations.length === 0) {
            document.getElementById('recommendations-empty').style.display = 'flex';
        }
        showToast('Show blacklisted');
    } catch (e) {
        showToast(`Failed to blacklist: ${e.message || e}`, 'error');
    }
}

async function refreshRecommendations() {
    const btn = document.getElementById('refresh-recommendations-btn');
    if (btn) {
        btn.disabled = true;
        btn.classList.add('spinning');
    }
    try {
        await api.post('/api/recommendations/refresh', {});
        showToast('Refresh started — this may take a minute');
    } catch (e) {
        showToast(`Refresh failed: ${e.message || e}`, 'error');
    } finally {
        if (btn) {
            btn.disabled = false;
            btn.classList.remove('spinning');
        }
    }
}

// ==============================================================================
// Add Series Page
// ==============================================================================
async function showAddSeriesPage() {
    state.currentView = 'add-series';
    hideAllPages();
    document.getElementById('page-add-series').classList.add('active');
    updateNav('series');

    document.getElementById('tvdb-search-input').value = '';
    document.getElementById('search-results').innerHTML = '';
    document.getElementById('tvdb-search-input').focus();
}

let searchTimer;
async function searchTVDB(query) {
    if (query.length < 2) {
        document.getElementById('search-results').innerHTML = '';
        return;
    }

    try {
        const results = await api.get(`/api/search?q=${encodeURIComponent(query)}`);
        const container = document.getElementById('search-results');

        if (results.length === 0) {
            container.innerHTML = '<p class="no-results text-muted">No results found</p>';
            return;
        }

        container.innerHTML = results.map(r => `
            <div class="search-result" onclick="addSeries(${r.id})">
                <div class="search-result-poster">
                    <img src="${esc(posterSrc(r.poster))}" alt="${esc(r.name)}">
                </div>
                <div class="search-result-info">
                    <div class="search-result-title">${esc(r.name)}</div>
                    <div class="search-result-year">${esc(r.year) || 'N/A'}</div>
                </div>
                <button class="btn btn-primary" onclick="event.stopPropagation(); addSeries(${r.id})">Add</button>
            </div>
        `).join('');
    } catch (e) {
        showToast('Search failed', 'error');
    }
}

async function addSeries(tvdbId) {
    try {
        await api.post('/api/series', { tvdb_id: tvdbId });
        navigate('/series');
        showToast('Series added');
    } catch (e) {
        showToast('Failed to add series', 'error');
    }
}

// ==============================================================================
// Alerts
// ==============================================================================
async function loadAlerts() {
    try {
        const alerts = await api.get('/api/alerts');
        const container = document.getElementById('alerts-container');
        if (!Array.isArray(alerts) || alerts.length === 0) {
            container.innerHTML = '';
            return;
        }
        container.innerHTML = alerts.map(alert => `
            <div class="alert alert-${alert.type.includes('error') ? 'error' : 'warning'}">
                <span class="alert-message">${esc(alert.message)}</span>
                <button class="alert-dismiss" onclick="dismissAlert(${alert.id})">x</button>
            </div>
        `).join('');
    } catch (e) {
        console.error('Failed to load alerts:', e);
    }
}

async function dismissAlert(id) {
    try {
        await api.post(`/api/alerts/${id}/dismiss`);
        loadAlerts();
    } catch (e) {
        showToast('Failed to dismiss alert', 'error');
    }
}

// ==============================================================================
// Scan
// ==============================================================================
async function triggerScan() {
    const btn = document.getElementById('scan-trigger');
    btn.classList.add('spinning');
    try {
        await api.post('/api/scan/trigger');
        showToast('Scan started');
        setTimeout(() => {
            if (state.currentView === 'series') {
                loadSeries();
            }
        }, 3000);
    } catch (e) {
        showToast('Scan failed', 'error');
    } finally {
        btn.classList.remove('spinning');
    }
}

// ==============================================================================
// Utility
// ==============================================================================
function hideAllPages() {
    document.querySelectorAll('.page').forEach(p => p.classList.remove('active'));
}

function updateNav(activePage) {
    document.querySelectorAll('.nav-link').forEach(link => {
        link.classList.toggle('active', link.dataset.page === activePage);
    });
}

// ==============================================================================
// Match Modal
// ==============================================================================
let matchSeriesId = null;
let matchSearchTimer = null;

function openMatchModal(seriesId) {
    matchSeriesId = seriesId;
    const modal = document.getElementById('match-modal');
    const input = document.getElementById('match-search-input');
    const results = document.getElementById('match-results');

    modal.classList.add('active');
    input.value = '';
    results.innerHTML = '';

    // Focus on search input
    setTimeout(() => input.focus(), 100);
}

function closeMatchModal() {
    const modal = document.getElementById('match-modal');
    modal.classList.remove('active');
    matchSeriesId = null;

    // Clear search
    document.getElementById('match-search-input').value = '';
    document.getElementById('match-results').innerHTML = '';
}

async function searchTVDBForMatch(query) {
    if (query.length < 2) {
        document.getElementById('match-results').innerHTML = '';
        return;
    }

    try {
        const results = await api.get(`/api/search?q=${encodeURIComponent(query)}`);
        const container = document.getElementById('match-results');

        if (results.length === 0) {
            container.innerHTML = '<p class="no-results text-muted">No results found</p>';
            return;
        }

        container.innerHTML = results.map(r => `
            <div class="match-result-item">
                <div class="match-result-poster">
                    <img src="${esc(posterSrc(r.poster))}" alt="${esc(r.name)}">
                </div>
                <div class="match-result-info">
                    <div class="match-result-title">${esc(r.name)}</div>
                    <div class="match-result-meta">
                        <span class="match-result-year">${esc(r.year) || 'N/A'}</span>
                        ${r.status ? `<span class="match-result-status">${esc(r.status)}</span>` : ''}
                    </div>
                    ${r.status ? `<div class="match-result-overview">${esc(r.status)}</div>` : ''}
                </div>
                <button class="btn btn-primary" onclick="matchSeries(${r.id})">
                    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                    </svg>
                    Link
                </button>
            </div>
        `).join('');
    } catch (e) {
        showToast('Search failed', 'error');
        console.error(e);
    }
}

async function matchSeries(tvdbId) {
    if (!matchSeriesId) {
        showToast('No series selected', 'error');
        return;
    }

    try {
        const result = await api.post(`/api/series/${matchSeriesId}/match`, { tvdb_id: tvdbId });

        closeMatchModal();

        // Check if this was a merge operation
        if (result.merged) {
            showToast(result.message || 'Seasons merged successfully');
            // Navigate to the merged-into series (current one was deleted)
            navigate(`/series/${result.id}`);
            return;
        }

        showToast('Series matched successfully');

        // Reload current view to show updated data
        if (state.currentView === 'series-detail' && state.currentSeriesId) {
            await loadSeriesDetail(state.currentSeriesId);
        } else {
            await loadSeries();
        }
    } catch (e) {
        showToast('Failed to match series', 'error');
        console.error(e);
    }
}

// ==============================================================================
// Initialize
// ==============================================================================
document.addEventListener('DOMContentLoaded', () => {
    // Setup event listeners
    document.getElementById('search-input')?.addEventListener('input', renderSeries);
    document.getElementById('sort-select')?.addEventListener('change', renderSeries);

    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            renderSeries();
        });
    });

    document.getElementById('tvdb-search-input')?.addEventListener('input', e => {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => searchTVDB(e.target.value), 300);
    });

    document.getElementById('match-search-input')?.addEventListener('input', e => {
        clearTimeout(matchSearchTimer);
        matchSearchTimer = setTimeout(() => searchTVDBForMatch(e.target.value), 300);
    });

    document.getElementById('add-series-btn')?.addEventListener('click', () => navigate('/add-series'));
    document.getElementById('empty-add-btn')?.addEventListener('click', () => navigate('/add-series'));
    document.getElementById('scan-trigger')?.addEventListener('click', triggerScan);
    document.getElementById('check-updates-btn')?.addEventListener('click', checkUpdates);
    document.getElementById('refresh-recommendations-btn')?.addEventListener('click', refreshRecommendations);
    document.getElementById('delete-series-btn')?.addEventListener('click', deleteSeries);
    document.getElementById('fix-match-btn')?.addEventListener('click', () => {
        if (state.currentSeriesId) openMatchModal(state.currentSeriesId);
    });

    // Back buttons
    document.getElementById('back-to-series')?.addEventListener('click', () => navigate('/series'));
    document.getElementById('back-to-series-from-season')?.addEventListener('click', () => {
        if (state.currentSeriesId) {
            navigate(`/series/${state.currentSeriesId}`);
        } else {
            navigate('/series');
        }
    });
    document.getElementById('back-from-add')?.addEventListener('click', () => navigate('/series'));

    // Process audio buttons
    document.getElementById('process-audio-btn')?.addEventListener('click', processSeasonAudio);
    document.getElementById('set-default-btn')?.addEventListener('click', setDefaultTrack);

    // Setup router
    window.addEventListener('hashchange', router);
    router();

    // Load alerts periodically
    loadAlerts();
    setInterval(loadAlerts, 30000);
});

// Make functions globally accessible
window.navigate = navigate;
window.selectTrack = selectTrack;
window.togglePreview = togglePreview;
window.processSeasonAudio = processSeasonAudio;
window.setDefaultTrack = setDefaultTrack;
window.addSeries = addSeries;
window.dismissAlert = dismissAlert;
window.deleteSeries = deleteSeries;
window.openMatchModal = openMatchModal;
window.closeMatchModal = closeMatchModal;
window.matchSeries = matchSeries;
window.updateSeasonVoice = updateSeasonVoice;
window.blacklistRecommendation = blacklistRecommendation;
window.refreshRecommendations = refreshRecommendations;
